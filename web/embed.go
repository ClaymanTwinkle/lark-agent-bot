//go:build !no_web

package web

import (
	"embed"
	"io/fs"
	"log/slog"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

//go:embed all:dist
var distFS embed.FS

func init() {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		slog.Warn("web: embedded dist directory unavailable; web admin disabled", "error", err)
		return
	}
	// A build that skipped the web build (plain go build, go install) embeds
	// only web/dist/.keep. That is expected, and this runs for every CLI
	// command, so it is not a warning; /web and the web command say the web
	// admin is not in this build.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		slog.Debug("web: web admin not built into this binary", "error", err)
		return
	}
	core.RegisterWebAssets(sub)
}
