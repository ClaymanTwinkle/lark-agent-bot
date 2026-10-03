package feishu

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// dmChats remembers each user's one-to-one chat with the bot. A bot menu
// click names only the user, so this is how it finds the session of the chat
// the menu was clicked in. A user's chat is learned when they open it or
// write in it, and kept in the data dir so it survives restarts.
type dmChats struct {
	mu    sync.Mutex
	path  string            // "" keeps the chats in memory only
	chats map[string]string // open_id → chat_id
}

func dmChatsPath(dataDir, platformName, project, appID string) string {
	return platformStatePath(dataDir, platformName, "dm_chats", project, appID)
}

// openDMChats loads the chats saved at path. With path "" nothing is loaded
// or saved.
func openDMChats(path string) *dmChats {
	d := &dmChats{path: path, chats: make(map[string]string)}
	if path == "" {
		return d
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("dm chats: read failed", "path", path, "error", err)
		}
		return d
	}
	var saved map[string]string
	if err := json.Unmarshal(data, &saved); err != nil {
		slog.Warn("dm chats: parse failed", "path", path, "error", err)
		return d
	}
	for userID, chatID := range saved {
		if userID != "" && chatID != "" {
			d.chats[userID] = chatID
		}
	}
	return d
}

// chatOf returns userID's chat with the bot, or "" while it is not known.
// A nil dmChats knows no chats.
func (d *dmChats) chatOf(userID string) string {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.chats[userID]
}

// remember records chatID as userID's chat with the bot.
func (d *dmChats) remember(userID, chatID string) {
	if d == nil || userID == "" || chatID == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.chats[userID] == chatID {
		return
	}
	d.chats[userID] = chatID
	d.saveLocked()
}

func (d *dmChats) saveLocked() {
	if d.path == "" {
		return
	}
	data, err := json.Marshal(d.chats)
	if err != nil {
		slog.Warn("dm chats: marshal failed", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
		slog.Warn("dm chats: mkdir failed", "path", d.path, "error", err)
		return
	}
	if err := core.AtomicWriteFile(d.path, data, 0o644); err != nil {
		slog.Warn("dm chats: write failed", "path", d.path, "error", err)
	}
}
