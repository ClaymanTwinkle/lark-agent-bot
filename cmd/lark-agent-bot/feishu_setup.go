package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// Both agents use the same platform contract. Keep this file reviewable and
// embeddable so installation does not depend on the current working directory.
//
//go:embed feishu_setup_template.json
var defaultFeishuSetupTemplate []byte

type setupIdentities struct {
	Tenant []string `json:"tenant,omitempty"`
	User   []string `json:"user,omitempty"`
}

type setupAddons struct {
	Preset bool            `json:"preset"`
	Scopes setupIdentities `json:"scopes"`
	Events struct {
		Items setupIdentities `json:"items"`
	} `json:"events"`
	Callbacks struct {
		Items []string `json:"items"`
	} `json:"callbacks"`
}

func loadSetupTemplate(path string) (*setupAddons, error) {
	data := defaultFeishuSetupTemplate
	if path != "" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read setup template: %w", err)
		}
	}
	var addons setupAddons
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&addons); err != nil {
		return nil, fmt.Errorf("decode setup template: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("setup template must contain one JSON object")
	}
	for _, list := range [][]string{addons.Scopes.Tenant, addons.Scopes.User, addons.Events.Items.Tenant, addons.Events.Items.User, addons.Callbacks.Items} {
		seen := map[string]bool{}
		for _, item := range list {
			if strings.TrimSpace(item) == "" || strings.TrimSpace(item) != item || seen[item] {
				return nil, fmt.Errorf("empty, padded or duplicate setup item %q", item)
			}
			seen[item] = true
		}
	}
	// These are required for setup verification and the bridge's basic journey.
	for _, scope := range []string{"application:application:self_manage", "im:message:send_as_bot", "im:message.p2p_msg:readonly", "im:message.group_at_msg:readonly", "im:resource", "im:message.reactions:write_only"} {
		if !slices.Contains(addons.Scopes.Tenant, scope) {
			return nil, fmt.Errorf("setup template missing required scope %q", scope)
		}
	}
	for _, event := range []string{"im.message.receive_v1", "im.message.recalled_v1", "application.bot.menu_v6"} {
		if !slices.Contains(addons.Events.Items.Tenant, event) {
			return nil, fmt.Errorf("setup template missing required event %q", event)
		}
	}
	if !slices.Contains(addons.Callbacks.Items, "card.action.trigger") {
		return nil, fmt.Errorf("setup template requires card.action.trigger")
	}
	return &addons, nil
}

// This wire format follows the official registration SDK: gzip JSON, then
// unpadded URL-safe base64 in the verification URL (not the begin POST body).
func setupRegistrationURL(raw string, opts registrationFlowOptions) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse registration URL: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("registration URL must use HTTPS")
	}
	if opts.Addons == nil {
		return "", fmt.Errorf("setup template is required")
	}
	data, err := json.Marshal(opts.Addons)
	if err != nil {
		return "", fmt.Errorf("encode setup template: %w", err)
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("addons", base64.RawURLEncoding.EncodeToString(buf.Bytes()))
	q.Set("createOnly", "true")
	q.Set("source", "lark-agent-bot")
	if opts.Name != "" {
		q.Set("name", opts.Name)
	}
	if opts.Description != "" {
		q.Set("desc", opts.Description)
	}
	if opts.Avatar != "" {
		q.Set("avatar", opts.Avatar)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type setupAPI struct {
	base   string
	client *http.Client
	token  string
}

// Never include raw API bodies/messages in errors: application details and
// token responses can include credentials.
func (a *setupAPI) request(method, path string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, a.base+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("setup API %s: %w", path, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("setup: close API response", "error", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("setup API %s: HTTP %d", path, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	var status struct {
		Code *int `json:"code"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return fmt.Errorf("setup API %s: invalid JSON", path)
	}
	if status.Code == nil {
		return fmt.Errorf("setup API %s: missing response code", path)
	}
	if *status.Code != 0 {
		return fmt.Errorf("setup API %s: code %d", path, *status.Code)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("setup API %s: invalid response", path)
	}
	return nil
}

type setupCheck struct {
	OwnerOpenID           string
	Missing               []string
	SubscriptionsVerified bool
}

func (a *setupAPI) check(appID, secret string, template *setupAddons) (*setupCheck, error) {
	var auth tenantTokenResponse
	if err := a.request(http.MethodPost, "/open-apis/auth/v3/tenant_access_token/internal", map[string]string{"app_id": appID, "app_secret": secret}, &auth); err != nil {
		return nil, err
	}
	if auth.TenantAccessToken == "" {
		return nil, fmt.Errorf("setup API: empty tenant token")
	}
	a.token = auth.TenantAccessToken
	var bot struct {
		Bot struct {
			OpenID string `json:"open_id"`
		} `json:"bot"`
	}
	if err := a.request(http.MethodGet, "/open-apis/bot/v3/info", nil, &bot); err != nil {
		return nil, err
	}
	if bot.Bot.OpenID == "" {
		return nil, fmt.Errorf("setup API: bot capability unavailable")
	}
	var scopes struct {
		Data struct {
			Scopes []struct {
				Name   string `json:"scope_name"`
				Type   string `json:"scope_type"`
				Status int    `json:"grant_status"`
			} `json:"scopes"`
		} `json:"data"`
	}
	if err := a.request(http.MethodGet, "/open-apis/application/v6/scopes", nil, &scopes); err != nil {
		return nil, err
	}
	granted := map[string]bool{}
	for _, s := range scopes.Data.Scopes {
		if s.Status == 1 {
			granted[s.Type+":"+s.Name] = true
		}
	}
	check := &setupCheck{}
	for _, group := range []struct {
		identity string
		scopes   []string
	}{{"tenant", template.Scopes.Tenant}, {"user", template.Scopes.User}} {
		for _, scope := range group.scopes {
			if !granted[group.identity+":"+scope] {
				check.Missing = append(check.Missing, group.identity+":"+scope)
			}
		}
	}
	if err := a.checkApplication(appID, bot.Bot.OpenID, template, check); err != nil {
		return check, err
	}
	return check, nil
}

func (a *setupAPI) checkApplication(appID, botID string, template *setupAddons, check *setupCheck) error {
	var details struct {
		Data struct {
			App *struct {
				Owner struct {
					ID string `json:"owner_id"`
				} `json:"owner"`
				Event *struct {
					Items []string `json:"subscribed_events"`
				} `json:"event"`
				Callback *struct {
					Items []string `json:"subscribed_callbacks"`
				} `json:"callback"`
			} `json:"app"`
		} `json:"data"`
	}
	path := "/open-apis/application/v6/applications/" + url.PathEscape(appID) + "?lang=en_us&user_id_type=open_id"
	if err := a.request(http.MethodGet, path, nil, &details); err != nil {
		return err
	}
	app := details.Data.App
	if app == nil {
		return fmt.Errorf("setup API: missing application details")
	}
	if strings.HasPrefix(app.Owner.ID, "ou_") && app.Owner.ID != botID {
		check.OwnerOpenID = app.Owner.ID
	}
	// Some PersonalAgent apps omit these fields; absence is UNKNOWN, not success.
	if app.Event != nil && app.Callback != nil {
		check.SubscriptionsVerified = true
		for _, item := range append(append([]string{}, template.Events.Items.Tenant...), template.Events.Items.User...) {
			if !slices.Contains(app.Event.Items, item) {
				check.Missing = append(check.Missing, "event:"+item)
			}
		}
		for _, item := range template.Callbacks.Items {
			if !slices.Contains(app.Callback.Items, item) {
				check.Missing = append(check.Missing, "callback:"+item)
			}
		}
	}
	return nil
}

func inspectSetup(appID, secret, platform string, template *setupAddons) (*setupCheck, error) {
	base := openFeishuBaseURL
	if platform == "lark" {
		base = openLarkBaseURL
	}
	api := &setupAPI{base: base, client: &http.Client{Timeout: 15 * time.Second}}
	return api.check(appID, secret, template)
}

func reportSetupCheck(check *setupCheck, err error) error {
	if check != nil && len(check.Missing) > 0 {
		return fmt.Errorf("%s", setupText(core.MsgSetupMissing, strings.Join(check.Missing, ", ")))
	}
	if err != nil {
		return err
	}
	if check == nil {
		return fmt.Errorf("setup check returned no result")
	}
	fmt.Println(setupText(core.MsgSetupChecked))
	if !check.SubscriptionsVerified {
		fmt.Println(setupText(core.MsgSetupSubscriptionsUnknown))
	}
	if check.OwnerOpenID == "" {
		return fmt.Errorf("%s", setupText(core.MsgSetupOwnerUnknown))
	}
	return nil
}

func readSetupConfig() (*config.Config, error) {
	data, err := os.ReadFile(config.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read setup config: %w", err)
	}
	var cfg config.Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse setup config: %w", err)
	}
	return &cfg, nil
}

func setupPlatform(cfg *config.Config, project string, index int) (*config.PlatformConfig, error) {
	if index < 0 {
		return nil, fmt.Errorf("platform index must be >= 0")
	}
	if index == 0 {
		index = 1
	}
	for _, p := range cfg.Projects {
		if p.Name != project {
			continue
		}
		for _, platform := range p.Platforms {
			if platform.Type != "feishu" && platform.Type != "lark" {
				continue
			}
			index--
			if index == 0 {
				return &platform, nil
			}
		}
		break
	}
	return nil, fmt.Errorf("project %q: platform not found", project)
}

// Reject impossible targets before asking the user to create an external app.
func preflightNewSetup(project string, index int) error {
	if index < 0 || index > 1 {
		if _, err := os.Stat(config.ConfigPath); os.IsNotExist(err) {
			return fmt.Errorf("platform index must be 0 or 1 for a new config")
		}
	}
	// Make an empty config before registration, so a first-ever project works.
	if err := ensureSetupConfig(); err != nil {
		return err
	}
	cfg, err := readSetupConfig()
	if err != nil {
		return err
	}
	if index < 0 {
		return fmt.Errorf("platform index must be >= 0")
	}
	platform, targetErr := setupPlatform(cfg, project, index)
	if targetErr == nil {
		id, _ := platform.Options["app_id"].(string)
		if id = strings.TrimSpace(id); id != "" && id != config.StarterAppID {
			return fmt.Errorf("%s", setupText(core.MsgSetupTargetOccupied, project))
		}
		return nil
	}
	if index > 1 {
		return targetErr
	}
	for _, p := range cfg.Projects {
		if p.Name == project {
			for _, platform := range p.Platforms {
				if platform.Type == "feishu" || platform.Type == "lark" {
					return targetErr
				}
			}
		}
	}
	return nil // first platform/project will be provisioned after authorization
}

// ensureSetupConfig creates an empty config file, and its directory, when there
// is none yet: on a fresh machine ~/.lark-agent-bot does not exist.
// Exclusive creation prevents concurrent setup from truncating a real file.
func ensureSetupConfig() error {
	if _, err := os.Stat(config.ConfigPath); !os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath), 0o755); err != nil {
		return fmt.Errorf("create setup config dir: %w", err)
	}
	file, err := os.OpenFile(config.ConfigPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create setup config: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("create setup config: %w", err)
	}
	// Setup writes the app secret here; 0600 does nothing on Windows.
	config.ProtectSecretFile(config.ConfigPath)
	return nil
}

func checkConfiguredSetup(project string, index int, template *setupAddons) error {
	cfg, err := readSetupConfig()
	if err != nil {
		return err
	}
	platform, err := setupPlatform(cfg, project, index)
	if err != nil {
		return err
	}
	id, _ := platform.Options["app_id"].(string)
	secret, _ := platform.Options["app_secret"].(string)
	if id == "" || secret == "" {
		return fmt.Errorf("project %q: missing app credentials", project)
	}
	check, err := inspectSetup(id, secret, platform.Type, template)
	return reportSetupCheck(check, err)
}
