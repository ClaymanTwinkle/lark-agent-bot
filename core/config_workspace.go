package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Switching between single- and multi-workspace mode rewrites the project's
// config and restarts: a running engine cannot turn multi-workspace mode off
// (SetMultiWorkspace is one-way), and either direction would rebuild the
// agent, the session store and the workspace bindings.

// configWorkspaceAction is the /config subcommand that switches the mode:
//
//	nav:/config workspace multi|single [dir]          confirmation card
//	act:/config workspace confirm multi|single <dir>  write and restart
//
// dir is base64 (RawURLEncoding) in card actions, since paths may hold spaces.
const configWorkspaceAction = "workspace"

// SetWorkspaceModeSaver sets how a workspace mode switch is written to the
// config file. Without it switching is unavailable.
func (e *Engine) SetWorkspaceModeSaver(fn func(multi bool, dir string) error) {
	e.workspaceModeSaver = fn
}

// SwitchWorkspaceMode writes the project's workspace mode to the config file:
// in multi-workspace mode dir is base_dir, in single-workspace mode work_dir.
// It takes effect at the next restart, which the caller requests. A working
// directory left by /dir is cleared, since it would override the new one.
func (e *Engine) SwitchWorkspaceMode(multi bool, dir string) error {
	if e.workspaceModeSaver == nil {
		return errors.New("switching the workspace mode is not available")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%q is not an absolute path", dir)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	if err := e.workspaceModeSaver(multi, filepath.Clean(dir)); err != nil {
		return fmt.Errorf("save workspace mode: %w", err)
	}
	if e.projectState != nil {
		e.projectState.ClearWorkDirOverride()
	}
	return nil
}

// workspaceModeDefaultDir is the directory the switch offers: for
// multi-workspace mode the parent of the current working directory, for
// single-workspace mode the project the viewer's chat is bound to. It is ""
// when there is none.
func (e *Engine) workspaceModeDefaultDir(multi bool, viewer *Message) string {
	if multi {
		wd, ok := e.agent.(interface{ GetWorkDir() string })
		if !ok {
			return ""
		}
		abs, err := filepath.Abs(wd.GetWorkDir())
		if err != nil {
			return ""
		}
		return filepath.Dir(abs)
	}
	if viewer == nil || e.workspaceBindings == nil {
		return ""
	}
	if b, _, usable := e.lookupEffectiveWorkspaceBinding(effectiveWorkspaceChannelKey(viewer)); usable {
		return b.Workspace
	}
	return ""
}

func encodeConfigDir(dir string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(dir))
}

func decodeConfigDir(s string) (string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return string(b), err == nil && len(b) > 0
}

// parseWorkspaceModeTarget reads "multi|single [dir]".
func parseWorkspaceModeTarget(args []string) (multi bool, rest []string, ok bool) {
	if len(args) == 0 {
		return false, nil, false
	}
	switch strings.ToLower(args[0]) {
	case "multi":
		return true, args[1:], true
	case "single":
		return false, args[1:], true
	}
	return false, nil, false
}

// renderWorkspaceModeRow adds the current mode and the switch button to the
// project page.
func (e *Engine) renderWorkspaceModeRow(cb *CardBuilder) {
	cb.Markdown(e.i18n.T(MsgConfigWorkspaceTitle))
	if e.multiWorkspace {
		cb.Markdown(e.i18n.Tf(MsgConfigWorkspaceMulti, e.baseDir))
		cb.Buttons(DefaultBtn(e.i18n.T(MsgConfigWorkspaceToSingle), "nav:/config workspace single"))
		return
	}
	wd := ""
	if a, ok := e.agent.(interface{ GetWorkDir() string }); ok {
		wd = a.GetWorkDir()
	}
	cb.Markdown(e.i18n.Tf(MsgConfigWorkspaceSingle, wd))
	cb.Buttons(DefaultBtn(e.i18n.T(MsgConfigWorkspaceToMulti), "nav:/config workspace multi"))
}

// renderWorkspaceModeConfirm is the card for "nav:/config workspace
// multi|single [b64 dir]": what the switch does, with confirm and cancel.
// Switching to single-workspace mode without a directory offers the
// projects under base_dir.
func (e *Engine) renderWorkspaceModeConfirm(args []string, viewer *Message) *Card {
	cb := NewCard().Title(e.i18n.T(MsgCardTitleConfig), "grey")
	cancel := DefaultBtn(e.i18n.T(MsgConfigCancel), "nav:/config project")
	multi, rest, ok := parseWorkspaceModeTarget(args)
	if !ok {
		return cb.Markdown(e.i18n.T(MsgConfigWorkspaceUsage)).Buttons(cancel).Build()
	}
	dir := ""
	if len(rest) > 0 {
		dir, _ = decodeConfigDir(rest[0])
	} else {
		dir = e.workspaceModeDefaultDir(multi, viewer)
	}
	if dir == "" && !multi && e.multiWorkspace {
		names, err := e.availableWorkspaces()
		if err == nil && len(names) > 0 {
			options := make([]CardSelectOption, 0, len(names))
			for _, name := range names {
				path := filepath.Join(e.baseDir, name)
				options = append(options, CardSelectOption{Text: name, Value: "nav:/config workspace single " + encodeConfigDir(path)})
			}
			return cb.Markdown(e.i18n.T(MsgConfigWorkspacePickDir)).
				Select(e.i18n.T(MsgConfigWorkspacePickPlaceholder), options, "").
				Note(e.i18n.Tf(MsgConfigWorkspaceOtherDir, "single")).
				Buttons(cancel).Build()
		}
	}
	modeWord := "single"
	if multi {
		modeWord = "multi"
	}
	if dir == "" {
		return cb.Markdown(e.i18n.Tf(MsgConfigWorkspaceOtherDir, modeWord)).Buttons(cancel).Build()
	}
	return cb.Markdown(e.workspaceModeConfirmText(multi, dir)).
		Note(e.i18n.Tf(MsgConfigWorkspaceOtherDir, modeWord)).
		Buttons(
			PrimaryBtn(e.i18n.T(MsgConfigWorkspaceConfirmButton), fmt.Sprintf("act:/config workspace confirm %s %s", modeWord, encodeConfigDir(dir))),
			cancel,
		).Build()
}

func (e *Engine) workspaceModeConfirmText(multi bool, dir string) string {
	if multi {
		return e.i18n.Tf(MsgConfigWorkspaceConfirmMulti, dir)
	}
	return e.i18n.Tf(MsgConfigWorkspaceConfirmSingle, dir)
}

// confirmWorkspaceMode handles "act:/config workspace confirm multi|single
// <b64 dir>": it writes the mode and requests a restart, returning the
// outcome to show.
func (e *Engine) confirmWorkspaceMode(args []string, sessionKey string) string {
	multi, rest, ok := parseWorkspaceModeTarget(args)
	if !ok || len(rest) != 1 {
		return e.i18n.T(MsgConfigWorkspaceUsage)
	}
	dir, ok := decodeConfigDir(rest[0])
	if !ok {
		return e.i18n.T(MsgConfigWorkspaceUsage)
	}
	return e.applyWorkspaceMode(multi, dir, sessionKey)
}

func (e *Engine) applyWorkspaceMode(multi bool, dir, sessionKey string) string {
	if err := e.SwitchWorkspaceMode(multi, dir); err != nil {
		return e.i18n.Tf(MsgError, err)
	}
	return e.i18n.T(MsgConfigWorkspaceSaved) + "\n" + e.configRestart(sessionKey)
}

// cmdConfigWorkspace handles "/config workspace multi|single [path]
// [confirm]". With cards it replies with the confirmation card; without them
// a trailing "confirm" switches.
func (e *Engine) cmdConfigWorkspace(p Platform, msg *Message, args []string) {
	multi, rest, ok := parseWorkspaceModeTarget(args)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgConfigWorkspaceUsage))
		return
	}
	confirm := len(rest) > 0 && strings.EqualFold(rest[len(rest)-1], "confirm")
	if confirm {
		rest = rest[:len(rest)-1]
	}
	modeWord := "single"
	if multi {
		modeWord = "multi"
	}
	dir := strings.TrimSpace(strings.Join(rest, " "))
	if dir == "" {
		dir = e.workspaceModeDefaultDir(multi, msg)
	}
	if supportsCards(p) {
		cardArgs := []string{modeWord}
		if dir != "" {
			cardArgs = append(cardArgs, encodeConfigDir(dir))
		}
		e.replyWithCard(p, msg.ReplyCtx, e.renderWorkspaceModeConfirm(cardArgs, msg))
		return
	}
	if dir == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgConfigWorkspaceOtherDir, modeWord))
		return
	}
	if !confirm {
		e.reply(p, msg.ReplyCtx, e.workspaceModeConfirmText(multi, dir)+"\n\n"+
			e.i18n.Tf(MsgConfigWorkspaceTextConfirm, modeWord, dir))
		return
	}
	e.reply(p, msg.ReplyCtx, e.applyWorkspaceMode(multi, dir, msg.SessionKey))
}

// replySingleWorkspaceMode answers /workspace in single-workspace mode: it
// says how to switch, with a button to the project page for admins.
func (e *Engine) replySingleWorkspaceMode(p Platform, msg *Message) {
	text := e.i18n.T(MsgWsSingleModeHint)
	if supportsCards(p) && e.isAdmin(msg.UserID) {
		e.replyWithCard(p, msg.ReplyCtx, NewCard().Markdown(text).
			Buttons(PrimaryBtn(e.i18n.T(MsgConfigPageProject), "nav:/config project")).Build())
		return
	}
	e.reply(p, msg.ReplyCtx, text)
}
