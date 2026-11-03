package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// The Claude instance's project is named "Mac Claude Code". `cron list` and
// `timer list` used to paste CC_PROJECT into the query string raw, the spaces
// broke the HTTP request line, and the API socket answered 400 Bad Request.

func TestListURL_EscapesProject(t *testing.T) {
	cases := map[string]string{
		"":                "/cron/list",
		"codex":           "/cron/list?project=codex",
		"Mac Claude Code": "/cron/list?project=Mac+Claude+Code",
		"a&b=c":           "/cron/list?project=a%26b%3Dc",
	}
	for project, want := range cases {
		if got := listURL("/cron/list", project); got != want {
			t.Errorf("listURL(%q) = %q, want %q", project, got, want)
		}
	}
}

// fakeListSocket serves an empty job list on <dataDir>/run/api.sock and records
// the project each request asked for.
func fakeListSocket(t *testing.T, path string) (dataDir string, projects chan string) {
	t.Helper()
	// Short temp dir: unix socket paths are capped at ~104 bytes on macOS and
	// t.TempDir() can exceed that.
	dir, err := os.MkdirTemp("", "lst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.MkdirAll(filepath.Join(dir, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "run", "api.sock"))
	if err != nil {
		t.Fatal(err)
	}
	projects = make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		projects <- r.URL.Query().Get("project")
		_, _ = io.WriteString(w, "[]")
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return dir, projects
}

// silenceStdout drops what the list commands print ("No scheduled tasks.").
func silenceStdout(t *testing.T) {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = devnull
	t.Cleanup(func() { os.Stdout = orig; _ = devnull.Close() })
}

func TestRunCronList_ProjectWithSpaces(t *testing.T) {
	dir, projects := fakeListSocket(t, "/cron/list")
	t.Setenv("CC_PROJECT", "Mac Claude Code")
	silenceStdout(t)
	runCronList([]string{"--data-dir", dir})
	if got := <-projects; got != "Mac Claude Code" {
		t.Fatalf("server saw project %q, want %q", got, "Mac Claude Code")
	}
}

func TestRunTimerList_ProjectWithSpaces(t *testing.T) {
	dir, projects := fakeListSocket(t, "/timer/list")
	t.Setenv("CC_PROJECT", "Mac Claude Code")
	silenceStdout(t)
	runTimerList([]string{"--data-dir", dir})
	if got := <-projects; got != "Mac Claude Code" {
		t.Fatalf("server saw project %q, want %q", got, "Mac Claude Code")
	}
}
