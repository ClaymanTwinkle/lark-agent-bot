package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseVersionOutput(t *testing.T) {
	cases := map[string]string{
		"lark-connect v0.2.3\ncommit:  abc\nbuilt:   x\n": "v0.2.3",
		"lark-connect v0.1.0+auto-review\n":               "v0.1.0+auto-review",
		"something else v1.0.0":                           "",
		"":                                                "",
	}
	for in, want := range cases {
		if got := parseVersionOutput(in); got != want {
			t.Errorf("parseVersionOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

// Regression: with two bots sharing one binary, the second /upgrade confirm
// tried to download and replace again and failed on Windows. When the binary
// on disk is already new enough, it must only restart.
func TestUpgradeAlreadyInstalled(t *testing.T) {
	orig := installedVersionFunc
	t.Cleanup(func() { installedVersionFunc = orig })

	cases := []struct {
		installed string
		err       error
		target    string
		want      bool
	}{
		{installed: "v0.2.3", target: "v0.2.3", want: true},
		{installed: "v0.3.0", target: "v0.2.3", want: true},
		{installed: "v0.2.2", target: "v0.2.3", want: false},
		{installed: "v0.1.0+auto-review", target: "v0.2.3", want: false},
		{err: errors.New("exec failed"), target: "v0.2.3", want: false},
	}
	for _, tc := range cases {
		installedVersionFunc = func() (string, error) { return tc.installed, tc.err }
		if _, got := upgradeAlreadyInstalled(tc.target); got != tc.want {
			t.Errorf("installed=%q err=%v target=%s: got %v, want %v", tc.installed, tc.err, tc.target, got, tc.want)
		}
	}
}

// Regression: "backup old binary: ... Access is denied" when the ".old"
// backup was still in use by another bot sharing the binary. The update must
// back up under a unique name instead of failing.
func TestReplaceBinaryAt_BackupInUse(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "lark-connect.exe")
	if err := os.WriteFile(execPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory cannot be removed, standing in for a ".old" file
	// locked by a running process.
	inUse := execPath + ".old"
	if err := os.MkdirAll(filepath.Join(inUse, "locked"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinaryAt(execPath, []byte("new binary")); err != nil {
		t.Fatalf("replaceBinaryAt: %v", err)
	}
	if b, _ := os.ReadFile(execPath); string(b) != "new binary" {
		t.Fatalf("installed binary = %q, want new binary", b)
	}
	if fi, err := os.Stat(inUse); err != nil || !fi.IsDir() {
		t.Fatalf("in-use backup was touched: %v", err)
	}
	matches, _ := filepath.Glob(execPath + ".old-*")
	if len(matches) != 1 {
		t.Fatalf("unique backups = %v, want exactly one", matches)
	}
	if b, _ := os.ReadFile(matches[0]); !strings.EqualFold(string(b), "old binary") {
		t.Fatalf("backup %s = %q, want old binary", matches[0], b)
	}
}

// fakeGitHub serves a releases API and a /releases/latest redirect page.
// apiStatus != 200 makes the API fail like GitHub's rate limit does.
func fakeGitHub(t *testing.T, apiStatus int, apiBody, latestLocation string) (apiURL, pageURL string, pageHits *atomic.Int32) {
	t.Helper()
	pageHits = new(atomic.Int32)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(apiStatus)
		_, _ = w.Write([]byte(apiBody))
	})
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		pageHits.Add(1)
		w.Header().Set("Location", latestLocation)
		w.WriteHeader(http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/api/releases", srv.URL + "/releases/latest", pageHits
}

// Regression: /upgrade failed with "API returned 403" once the shared egress
// IP used up GitHub's unauthenticated quota. It must fall back to the release
// page redirect.
func TestCheckForUpdate_FallsBackWhenAPIRateLimited(t *testing.T) {
	const loc = "https://github.com/ClaymanTwinkle/lark-connect/releases/tag/v0.2.1"
	apiURL, pageURL, _ := fakeGitHub(t, http.StatusForbidden, `{"message":"API rate limit exceeded"}`, loc)

	got, err := checkForUpdateFrom("v0.2.0", apiURL, pageURL)
	if err != nil {
		t.Fatalf("checkForUpdateFrom: %v", err)
	}
	if got == nil || got.TagName != "v0.2.1" {
		t.Fatalf("release = %+v, want tag v0.2.1", got)
	}
	if got.Body != loc {
		t.Errorf("Body = %q, want release page %q", got.Body, loc)
	}

	up, err := checkForUpdateFrom("v0.2.1", apiURL, pageURL)
	if err != nil || up != nil {
		t.Fatalf("same version via fallback: release = %+v, err = %v, want nil, nil", up, err)
	}
}

func TestCheckForUpdate_UsesAPIWhenAvailable(t *testing.T) {
	body := `[{"tag_name":"v0.2.1","body":"notes"},{"tag_name":"v0.3.0-beta.1"},{"tag_name":"v0.1.0"}]`
	apiURL, pageURL, pageHits := fakeGitHub(t, http.StatusOK, body, "")

	got, err := checkForUpdateFrom("v0.2.0", apiURL, pageURL)
	if err != nil {
		t.Fatalf("checkForUpdateFrom: %v", err)
	}
	if got == nil || got.TagName != "v0.3.0-beta.1" {
		t.Fatalf("release = %+v, want newest tag v0.3.0-beta.1", got)
	}
	if n := pageHits.Load(); n != 0 {
		t.Errorf("fallback page hit %d times, want 0", n)
	}
}

func TestCheckForUpdate_ErrorsWhenFallbackHasNoTag(t *testing.T) {
	apiURL, pageURL, _ := fakeGitHub(t, http.StatusForbidden, `{}`,
		"https://github.com/ClaymanTwinkle/lark-connect/releases")

	if got, err := checkForUpdateFrom("v0.2.0", apiURL, pageURL); err == nil {
		t.Fatalf("release = %+v, want an error", got)
	}
}

func TestSemverCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int // >0, <0, or 0
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.0.0", "v1.0.1", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.1.0", "v1.0.9", 1},

		// pre-release vs release
		{"v1.0.0", "v1.0.0-beta.1", 1},
		{"v1.0.0-beta.1", "v1.0.0", -1},

		// pre-release ordering
		{"v1.0.0-beta.2", "v1.0.0-beta.1", 1},
		{"v1.0.0-beta.1", "v1.0.0-beta.2", -1},
		{"v1.0.0-beta.1", "v1.0.0-beta.1", 0},

		// different pre-release prefixes
		{"v1.0.0-rc.1", "v1.0.0-beta.1", 1}, // "rc" > "beta" lexicographically
		{"v1.0.0-alpha.1", "v1.0.0-beta.1", -1},

		// without 'v' prefix
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
	}

	for _, tt := range tests {
		got := semverCompare(tt.a, tt.b)
		if (tt.want > 0 && got <= 0) || (tt.want < 0 && got >= 0) || (tt.want == 0 && got != 0) {
			t.Errorf("semverCompare(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	s := parseSemver("v1.2.3-beta.4")
	if s.major != 1 || s.minor != 2 || s.patch != 3 {
		t.Errorf("parsed %+v, want 1.2.3", s)
	}
	if s.pre != "beta.4" {
		t.Errorf("pre = %q, want beta.4", s.pre)
	}
	if s.preNum != 4 {
		t.Errorf("preNum = %d, want 4", s.preNum)
	}
}

func TestParseSemver_NoPreRelease(t *testing.T) {
	s := parseSemver("v2.0.0")
	if s.major != 2 || s.minor != 0 || s.patch != 0 {
		t.Errorf("parsed %+v, want 2.0.0", s)
	}
	if s.pre != "" {
		t.Errorf("pre = %q, want empty", s.pre)
	}
}

func TestParseSemver_Invalid(t *testing.T) {
	s := parseSemver("not-a-version")
	if s.major != 0 && s.minor != 0 && s.patch != 0 {
		t.Errorf("expected zero semver for invalid input, got %+v", s)
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"1.0.0", "v1.0.0"},
		{"v1.0.0", "v1.0.0"},
		{" v1.0.0 ", "v1.0.0"},
		{"  2.3.4", "v2.3.4"},
	}
	for _, tt := range tests {
		got := normalizeVersion(tt.in)
		if got != tt.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
