package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const workspacePickerPageSize = 8

// List only immediate, visible directories. Do not follow links out of the
// configured project root. Actions carry names, never unstable list indexes.
func (e *Engine) availableWorkspaces() ([]string, error) {
	entries, err := os.ReadDir(e.baseDir)
	if err != nil {
		return nil, fmt.Errorf("list workspace directories: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

func (e *Engine) replyWorkspacePicker(p Platform, msg *Message, page int) {
	card, err := e.workspacePickerCard(effectiveWorkspaceChannelKey(msg), page)
	if err != nil {
		slog.Warn("workspace picker failed", "error", err)
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	e.replyWithCard(p, msg.ReplyCtx, card)
}

func (e *Engine) workspacePickerCard(channelKey string, page int) (*Card, error) {
	names, err := e.availableWorkspaces()
	if err != nil {
		return nil, err
	}
	pages := max(1, (len(names)+workspacePickerPageSize-1)/workspacePickerPageSize)
	page = max(1, min(page, pages))
	cb := NewCard().Title(e.i18n.Tf(MsgWsPickerTitle, len(names), page, pages), "blue")
	cb.Markdown(e.i18n.Tf(MsgWsPickerRoot, e.baseDir))
	current := ""
	if binding, _, usable := e.lookupEffectiveWorkspaceBinding(channelKey); usable {
		current = binding.Workspace
		// Pin before pagination, preserving the order of every other project.
		for i, name := range names {
			if normalizeWorkspacePath(filepath.Join(e.baseDir, name)) == current {
				copy(names[1:i+1], names[:i])
				names[0] = name
				break
			}
		}
		cb.Markdown(e.i18n.Tf(MsgWsPickerCurrent, current))
	} else {
		cb.Markdown(e.i18n.T(MsgWsNoBinding))
	}
	if len(names) == 0 {
		cb.Markdown(e.i18n.T(MsgWsPickerEmpty))
	}
	start := (page - 1) * workspacePickerPageSize
	for _, name := range names[start:min(start+workspacePickerPageSize, len(names))] {
		label, style := e.i18n.T(MsgWsPickerSelect), "default"
		if normalizeWorkspacePath(filepath.Join(e.baseDir, name)) == current {
			label, style = e.i18n.T(MsgWsPickerSelected), "primary"
		}
		value := base64.RawURLEncoding.EncodeToString([]byte(name))
		cb.ListItemBtn(name, label, style, "act:/workspace select "+value)
	}
	var buttons []CardButton
	if page > 1 {
		buttons = append(buttons, DefaultBtn(e.i18n.T(MsgCardPrev), fmt.Sprintf("nav:/workspace available %d", page-1)))
	}
	if page < pages {
		buttons = append(buttons, DefaultBtn(e.i18n.T(MsgCardNext), fmt.Sprintf("nav:/workspace available %d", page+1)))
	}
	cb.Buttons(buttons...)
	cb.Note(e.i18n.T(MsgWsPickerHint))
	cb.Buttons(DefaultBtn(e.i18n.T(MsgCardBack), "nav:/help system"))
	return cb.Build(), nil
}

var errWorkspaceSelectionStale = errors.New("workspace selection is no longer available")

// Re-read candidates on click; names survive pagination and preserve spaces.
func (e *Engine) bindWorkspaceSelection(msg *Message, args []string) (string, int, error) {
	if len(args) != 1 {
		return "", 1, errWorkspaceSelectionStale
	}
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(args[0])
	names, err := e.availableWorkspaces()
	if err != nil {
		return "", 1, err
	}
	if decodeErr == nil {
		for _, name := range names {
			if name == string(decoded) {
				key := effectiveWorkspaceChannelKey(msg)
				channelName := ""
				if binding, _, usable := e.lookupEffectiveWorkspaceBinding(key); usable {
					channelName = binding.ChannelName
				}
				e.workspaceBindings.Bind("project:"+e.name, key, channelName, normalizeWorkspacePath(filepath.Join(e.baseDir, name)))
				// The newly bound project is now the first item on page one.
				return name, 1, nil
			}
		}
	}
	return "", 1, errWorkspaceSelectionStale
}

func (e *Engine) workspaceSelectionError(err error) string {
	if errors.Is(err, errWorkspaceSelectionStale) {
		return e.i18n.T(MsgWsPickerStale)
	}
	slog.Warn("workspace selection failed", "error", err)
	return e.i18n.Tf(MsgWsResolutionError, err)
}

// Retain text commands and buttons from cards sent by older versions.
func (e *Engine) selectWorkspaceFromPicker(p Platform, msg *Message, args []string) {
	name, _, err := e.bindWorkspaceSelection(msg, args)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.workspaceSelectionError(err))
		return
	}
	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsBindSuccess, name))
}

func (e *Engine) handleCardNavWithContext(action string, msg *Message) *Card {
	prefix, command, _ := strings.Cut(action, ":")
	args := strings.Fields(command)
	if len(args) == 0 || args[0] != "/workspace" {
		return e.cardNav(action, msg.SessionKey, msg)
	}
	errorCard := func(text string) *Card {
		return NewCard().Markdown(text).Buttons(DefaultBtn(e.i18n.T(MsgCardBack), "nav:/workspace bind")).Build()
	}
	if !e.multiWorkspace {
		return errorCard(e.i18n.T(MsgWsNotEnabled))
	}
	// Match command authorization using the clicker's identity, not the user
	// who originally opened the card (shared group/topic cards can differ).
	if e.effectiveDisabledCmds(msg.UserID)["workspace"] {
		return errorCard(e.i18n.Tf(MsgCommandDisabled, "/workspace"))
	}
	page, notice := 1, ""
	switch {
	case prefix == "nav" && len(args) == 2 && args[1] == "bind":
	case prefix == "nav" && len(args) == 3 && args[1] == "available":
		page, _ = strconv.Atoi(args[2])
	case prefix == "act" && len(args) == 3 && args[1] == "select":
		name, selectedPage, err := e.bindWorkspaceSelection(msg, args[2:])
		if err != nil {
			return errorCard(e.workspaceSelectionError(err))
		}
		page, notice = selectedPage, e.i18n.Tf(MsgWsBindSuccess, name)
	default:
		return errorCard(e.i18n.T(MsgWsUsage))
	}
	card, err := e.workspacePickerCard(effectiveWorkspaceChannelKey(msg), page)
	if err != nil {
		return errorCard(e.workspaceSelectionError(err))
	}
	if notice != "" {
		card.Elements = append([]CardElement{CardMarkdown{Content: notice}}, card.Elements...)
	}
	return card
}
