package codex

import "testing"

func TestDeveloperInstructionsAppendsPlatformPrompt(t *testing.T) {
	for _, tc := range []struct{ tools, platform, want string }{
		{"tools", "", "tools"},
		{"", "peers", "peers"},
		{"tools\n", "\npeers", "tools\n\npeers"},
	} {
		if got := developerInstructions(tc.tools, tc.platform); got != tc.want {
			t.Errorf("developerInstructions(%q, %q) = %q, want %q", tc.tools, tc.platform, got, tc.want)
		}
	}
}
