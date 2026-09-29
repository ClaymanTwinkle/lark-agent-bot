package main

import "testing"

// Regression: Codex strips *KEY* variables from the commands it runs, so
// `lark-connect send` never saw CC_SESSION_KEY there and could post to the
// wrong chat. CC_SESSION must win; CC_SESSION_KEY stays as the fallback.
func TestSessionKeyFromEnv(t *testing.T) {
	cases := []struct {
		name, session, sessionKey, want string
	}{
		{name: "new name only", session: "feishu:chat:user", want: "feishu:chat:user"},
		{name: "old name only", sessionKey: "feishu:old", want: "feishu:old"},
		{name: "new name wins", session: "feishu:new", sessionKey: "feishu:old", want: "feishu:new"},
		{name: "blank new name falls back", session: "  ", sessionKey: "feishu:old", want: "feishu:old"},
		{name: "neither", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CC_SESSION", tc.session)
			t.Setenv("CC_SESSION_KEY", tc.sessionKey)
			if got := sessionKeyFromEnv(); got != tc.want {
				t.Fatalf("sessionKeyFromEnv() = %q, want %q", got, tc.want)
			}
		})
	}
}
