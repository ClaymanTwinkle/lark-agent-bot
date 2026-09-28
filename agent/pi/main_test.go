package pi

import (
	"os"
	"testing"

	"github.com/ClaymanTwinkle/lark-connect/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.RunCLI()
	os.Exit(m.Run())
}
