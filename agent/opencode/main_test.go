package opencode

import (
	"os"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.RunCLI()
	os.Exit(m.Run())
}
