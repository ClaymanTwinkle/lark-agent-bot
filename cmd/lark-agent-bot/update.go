package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

const (
	githubRepo   = "ClaymanTwinkle/lark-agent-bot"
	githubAPI    = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	githubAllAPI = "https://api.github.com/repos/" + githubRepo + "/releases"
	downloadBase = "https://github.com/" + githubRepo + "/releases/download"
)

// cachedLatestVersion 缓存最新版本信息，避免频繁请求API
var cachedLatestVersion struct {
	version   string
	timestamp time.Time
	mu        sync.RWMutex
}

// versionCheckTTL is how old a known latest version may be and still be
// shown in the update hint. An old entry is still a correct hint: the
// release it names exists and is newer than this binary. How often GitHub
// is asked is updateCheckInterval.
const versionCheckTTL = 7 * 24 * time.Hour

type githubRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Prerelease bool   `json:"prerelease"`
}

// fetchLatestStableReleaseAsync 异步获取最新稳定版本（非pre-release）. It asks
// GitHub only when no lark-agent-bot process on this host has done so in the
// last updateCheckInterval (see claimUpdateCheck).
func fetchLatestStableReleaseAsync() {
	path := updateCheckPath()
	if !claimUpdateCheck(path, time.Now()) {
		return
	}
	go func() {
		release, err := fetchLatestStableRelease()
		if err != nil || release == nil || release.TagName == "" {
			slog.Debug("update check failed", "error", err)
			return
		}
		now := time.Now()
		// 缓存结果
		cachedLatestVersion.mu.Lock()
		cachedLatestVersion.version = release.TagName
		cachedLatestVersion.timestamp = now
		cachedLatestVersion.mu.Unlock()
		recordLatestVersion(path, release.TagName, now)
	}()
}

// checkUpdateAsync 启动异步版本检查（不阻塞）
func checkUpdateAsync() {
	// dev版本不检查
	if version == "dev" || version == "" {
		return
	}
	// Only printUsage shows the update hint. Subcommands never do, and
	// update / check-update ask GitHub themselves; agents run subcommands
	// (send, cron, timer, ...) many times an hour.
	if invokesSubcommand(os.Args[1:]) {
		return
	}
	loadCachedLatestVersion(updateCheckPath())
	fetchLatestStableReleaseAsync()
}

// getUpdateHintIfAvailable returns an update hint only from cache (never blocks on network).
// Call checkUpdateAsync() early to populate the cache in the background.
func getUpdateHintIfAvailable() string {
	if version == "dev" || version == "" {
		return ""
	}

	cachedLatestVersion.mu.RLock()
	cachedVer := cachedLatestVersion.version
	cachedTime := cachedLatestVersion.timestamp
	cachedLatestVersion.mu.RUnlock()

	if cachedVer == "" || time.Since(cachedTime) > versionCheckTTL {
		// Cache miss or expired — trigger async refresh, don't block
		fetchLatestStableReleaseAsync()
		return ""
	}

	if isNewer(cachedVer, version) {
		return fmt.Sprintf("\n📦 Update available: %s → %s  (run: lark-agent-bot update)\n", version, cachedVer)
	}
	return ""
}

func runUpdate() {
	pre := false
	for _, arg := range os.Args[2:] {
		if arg == "--pre" || arg == "--beta" {
			pre = true
		}
	}

	fmt.Printf("lark-agent-bot %s\n", version)
	if pre {
		fmt.Println("Checking for updates (including pre-releases)...")
	} else {
		fmt.Println("Checking for updates...")
	}

	release, err := fetchRelease(pre)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking updates: %v\n", err)
		os.Exit(1)
	}

	latest := release.TagName
	if !isNewer(latest, version) {
		fmt.Printf("Already up to date (%s >= %s).\n", version, latest)
		return
	}

	label := latest
	if release.Prerelease {
		label += " (pre-release)"
	}
	fmt.Printf("New version available: %s → %s\n", version, label)

	// Try archive format first (tar.gz/zip), then bare binary as fallback
	archiveAsset := archiveAssetName(latest)
	archiveURL := fmt.Sprintf("%s/%s/%s", downloadBase, latest, archiveAsset)

	fmt.Printf("Downloading %s ...\n", archiveURL)

	tmpFile, err := downloadToTemp(archiveURL)
	needExtract := err == nil

	if err != nil {
		// Fallback: try bare binary format (older releases)
		binaryAsset := binaryAssetName(latest)
		binaryURL := fmt.Sprintf("%s/%s/%s", downloadBase, latest, binaryAsset)
		fmt.Printf("Archive not found, trying bare binary %s ...\n", binaryURL)

		tmpFile, err = downloadToTemp(binaryURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
			os.Exit(1)
		}
	}

	if needExtract {
		// Downloaded an archive - extract binary
		extracted, extractErr := extractBinaryFromArchive(tmpFile, archiveAsset)
		os.Remove(tmpFile) // clean up archive
		if extractErr != nil {
			fmt.Fprintf(os.Stderr, "Extract failed: %v\n", extractErr)
			os.Exit(1)
		}
		tmpFile = extracted
	}
	defer os.Remove(tmpFile)

	newBinary, err := os.ReadFile(tmpFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: read downloaded binary: %v\n", err)
		os.Exit(1)
	}
	installedPath, err := core.InstallBinary(newBinary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		os.Exit(1)
	}

	syncNpmPackageVersion(installedPath, strings.TrimPrefix(latest, "v"))

	fmt.Printf("Updated to %s (%s)\n", latest, installedPath)
	if execPath, err := os.Executable(); err == nil && !strings.EqualFold(filepath.Clean(execPath), filepath.Clean(installedPath)) {
		fmt.Printf("The binary now uses the standard name. Scripts or services that started %s must start %s instead.\n", execPath, installedPath)
	}
	fmt.Println("Restart lark-agent-bot to use the new version.")
}

// fetchRelease returns the latest release. If pre=true, includes pre-releases.
func fetchRelease(pre bool) (*githubRelease, error) {
	if pre {
		return fetchLatestPreRelease()
	}
	return fetchLatestStableRelease()
}

// fetchLatestPreRelease fetches the newest release (including pre-releases) from GitHub.
func fetchLatestPreRelease() (*githubRelease, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", githubAllAPI+"?per_page=10", nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	core.SetGitHubAuth(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}

	if len(releases) == 0 {
		return nil, fmt.Errorf("no releases found")
	}

	// Return the first (newest) release, which may be a pre-release
	return &releases[0], nil
}

// fetchLatestStableRelease fetches the latest stable release (no pre-releases).
func fetchLatestStableRelease() (*githubRelease, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", githubAPI, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	core.SetGitHubAuth(req)

	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			var release githubRelease
			if err := json.NewDecoder(resp.Body).Decode(&release); err == nil {
				return &release, nil
			}
		}
	}

	// Fallback: follow redirect from /releases/latest to extract tag
	latestURL := "https://github.com/" + githubRepo + "/releases/latest"
	noRedirect := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp2, err := noRedirect.Get(latestURL)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp2.Body.Close()

	loc := resp2.Header.Get("Location")
	if loc == "" {
		return nil, fmt.Errorf("no release found")
	}
	parts := strings.Split(loc, "/tag/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("unexpected redirect: %s", loc)
	}
	return &githubRelease{TagName: parts[1], HTMLURL: loc}, nil
}

func binaryAssetName(tag string) string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	name := fmt.Sprintf("lark-agent-bot-%s-%s-%s", tag, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func archiveAssetName(tag string) string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	base := fmt.Sprintf("lark-agent-bot-%s-%s-%s", tag, goos, goarch)
	if goos == "windows" {
		return base + ".zip"
	}
	return base + ".tar.gz"
}

// extractBinaryFromArchive extracts the lark-agent-bot binary from a .tar.gz or .zip archive.
func extractBinaryFromArchive(archivePath, archiveName string) (string, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		return extractFromZip(archivePath)
	}
	return extractFromTarGz(archivePath)
}

func extractFromTarGz(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if strings.HasPrefix(hdr.Name, "lark-agent-bot") {
			tmp, err := os.CreateTemp("", "lark-agent-bot-update-*")
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(tmp, tr); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return "", fmt.Errorf("extract: %w", err)
			}
			tmp.Close()
			return tmp.Name(), nil
		}
	}
	return "", fmt.Errorf("binary not found in archive")
}

func extractFromZip(archivePath string) (string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, "lark-agent-bot") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		tmp, err := os.CreateTemp("", "lark-agent-bot-update-*")
		if err != nil {
			rc.Close()
			return "", err
		}
		if _, err := io.Copy(tmp, rc); err != nil {
			tmp.Close()
			rc.Close()
			os.Remove(tmp.Name())
			return "", fmt.Errorf("extract: %w", err)
		}
		rc.Close()
		tmp.Close()
		return tmp.Name(), nil
	}
	return "", fmt.Errorf("binary not found in archive")
}

func downloadToTemp(url string) (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "lark-agent-bot-update-*")
	if err != nil {
		return "", err
	}

	size, err := io.Copy(tmp, resp.Body)
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("write: %w", err)
	}
	tmp.Close()

	fmt.Printf("Downloaded %.1f MB\n", float64(size)/1024/1024)
	return tmp.Name(), nil
}

func checkUpdate() {
	pre := false
	for _, arg := range os.Args[2:] {
		if arg == "--pre" || arg == "--beta" {
			pre = true
		}
	}

	release, err := fetchRelease(pre)
	if err != nil {
		return
	}
	if isNewer(release.TagName, version) {
		hint := "lark-agent-bot update"
		if release.Prerelease {
			hint = "lark-agent-bot update --pre"
		}
		fmt.Fprintf(os.Stderr, "Update available: %s → %s (run: %s)\n", version, release.TagName, hint)
	}
}

// isNewer returns true if latest represents a newer release than current.
// Handles semver tags (v1.2.3), pre-release tags (v1.2.3-beta.1, v1.2.3-rc.1),
// and dev builds (v1.2.3-10-gHASH).
func isNewer(latest, current string) bool {
	if latest == "" || current == "" {
		return false
	}
	if strings.HasPrefix(current, "dev") {
		return true
	}

	l := strings.TrimPrefix(latest, "v")
	c := strings.TrimPrefix(current, "v")

	lBase, lPre, _ := strings.Cut(l, "-")
	cBase, cPre, _ := strings.Cut(c, "-")

	lParts := strings.Split(lBase, ".")
	cParts := strings.Split(cBase, ".")

	for i := 0; i < len(lParts) || i < len(cParts); i++ {
		var lv, cv int
		if i < len(lParts) {
			_, _ = fmt.Sscanf(lParts[i], "%d", &lv)
		}
		if i < len(cParts) {
			_, _ = fmt.Sscanf(cParts[i], "%d", &cv)
		}
		if lv > cv {
			return true
		}
		if lv < cv {
			return false
		}
	}

	// Same base version — compare pre-release suffix
	// No pre-release beats a pre-release (1.2.0 > 1.2.0-beta.1)
	if cPre != "" && lPre == "" {
		return true
	}
	if cPre == "" && lPre != "" {
		return false
	}
	// Both have pre-release: split on "." and compare each segment
	// numerically where possible so beta.10 > beta.2.
	if lPre != "" && cPre != "" {
		return comparePreRelease(lPre, cPre) > 0
	}

	return false
}

// comparePreRelease compares two pre-release strings segment by segment.
// Numeric segments are compared as integers; non-numeric segments are
// compared lexicographically. Returns >0 if a is greater, <0 if b is
// greater, 0 if equal.
func comparePreRelease(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	max := len(aParts)
	if len(bParts) > max {
		max = len(bParts)
	}
	for i := 0; i < max; i++ {
		var ap, bp string
		if i < len(aParts) {
			ap = aParts[i]
		}
		if i < len(bParts) {
			bp = bParts[i]
		}

		var an, bn int
		aN, _ := fmt.Sscanf(ap, "%d", &an)
		bN, _ := fmt.Sscanf(bp, "%d", &bn)
		aIsNum := aN == 1 && fmt.Sprintf("%d", an) == ap
		bIsNum := bN == 1 && fmt.Sprintf("%d", bn) == bp

		if aIsNum && bIsNum {
			if an != bn {
				return an - bn
			}
			continue
		}
		// Non-numeric: lexicographic
		if ap < bp {
			return -1
		}
		if ap > bp {
			return 1
		}
	}
	return 0
}

// syncNpmPackageVersion detects if the binary lives inside an npm package
// (node_modules/lark-agent-bot/bin/) and updates the package.json version to
// match the newly installed binary. Without this, the npm wrapper's run.js
// would see a version mismatch and re-download the old version on next run.
func syncNpmPackageVersion(execPath, newVer string) {
	binDir := filepath.Dir(execPath)
	if filepath.Base(binDir) != "bin" {
		return
	}
	pkgDir := filepath.Dir(binDir)
	pkgJSON := filepath.Join(pkgDir, "package.json")

	data, err := os.ReadFile(pkgJSON)
	if err != nil {
		return
	}

	var pkg map[string]any
	if err := json.Unmarshal(data, &pkg); err != nil {
		return
	}

	name, _ := pkg["name"].(string)
	if name != "lark-agent-bot" {
		return
	}

	oldVer, _ := pkg["version"].(string)
	// Normalize both sides by stripping optional "v" prefix before comparing.
	// package.json may store "v1.0.0" while newVer is already stripped to "1.0.0".
	oldNorm := strings.TrimPrefix(oldVer, "v")
	newNorm := strings.TrimPrefix(newVer, "v")
	if oldNorm == newNorm {
		return
	}

	pkg["version"] = newVer
	out, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return
	}
	out = append(out, '\n')
	if err := os.WriteFile(pkgJSON, out, 0o644); err != nil {
		slog.Warn("update: failed to sync npm package.json version", "error", err)
		fmt.Println("⚠️  Note: npm package version not synced. If the next run re-downloads an old version,")
		fmt.Println("   please run: npm update -g lark-agent-bot")
	} else {
		slog.Debug("update: synced npm package.json version", "old", oldVer, "new", newVer)
	}
}

// warnNonStandardBinaryName logs when this executable is not named
// lark-agent-bot[.exe]. The engine puts its directory on the agents' PATH, so
// under any other name (e.g. the versioned name from an older release
// archive) agents cannot run `lark-agent-bot send`, `cron` or `timer`. The next
// update installs the standard name.
func warnNonStandardBinaryName() {
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	if strings.EqualFold(filepath.Base(execPath), core.StandardBinaryName()) {
		return
	}
	slog.Warn("executable is not named "+core.StandardBinaryName()+
		"; agents cannot run lark-agent-bot commands (send, cron, timer) until it is renamed or updated",
		"path", execPath)
}
