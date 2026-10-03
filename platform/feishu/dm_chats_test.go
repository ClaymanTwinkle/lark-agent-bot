package feishu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDMChats_SavedAcrossOpens(t *testing.T) {
	path := dmChatsPath(t.TempDir(), "feishu", "proj", "cli_x")
	d := openDMChats(path)
	d.remember("ou_a", "oc_a")
	d.remember("ou_b", "oc_b")
	d.remember("ou_a", "oc_a2")

	reopened := openDMChats(path)
	for userID, want := range map[string]string{"ou_a": "oc_a2", "ou_b": "oc_b", "ou_none": ""} {
		if got := reopened.chatOf(userID); got != want {
			t.Errorf("chatOf(%s) = %q, want %q", userID, got, want)
		}
	}
}

func TestDMChats_IgnoresEmptyIDs(t *testing.T) {
	path := dmChatsPath(t.TempDir(), "feishu", "proj", "cli_x")
	d := openDMChats(path)
	d.remember("", "oc_a")
	d.remember("ou_a", "")
	if got := d.chatOf("ou_a"); got != "" {
		t.Fatalf("chatOf(ou_a) = %q, want none", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file written for empty IDs: stat error = %v", err)
	}
}

func TestDMChats_UnreadableFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dm_chats.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := openDMChats(path)
	if got := d.chatOf("ou_a"); got != "" {
		t.Fatalf("chatOf(ou_a) = %q, want none", got)
	}
	d.remember("ou_a", "oc_a")
	if got := openDMChats(path).chatOf("ou_a"); got != "oc_a" {
		t.Fatalf("chatOf(ou_a) after rewrite = %q, want oc_a", got)
	}
}

func TestDMChats_WithoutDataDirKeepsChatsInMemory(t *testing.T) {
	if path := dmChatsPath("", "feishu", "proj", "cli_x"); path != "" {
		t.Fatalf("path without data dir = %q", path)
	}
	d := openDMChats("")
	d.remember("ou_a", "oc_a")
	if got := d.chatOf("ou_a"); got != "oc_a" {
		t.Fatalf("chatOf(ou_a) = %q, want oc_a", got)
	}

	// A platform built without the constructor has no dmChats.
	var none *dmChats
	none.remember("ou_a", "oc_a")
	if got := none.chatOf("ou_a"); got != "" {
		t.Fatalf("nil chatOf(ou_a) = %q, want none", got)
	}
}
