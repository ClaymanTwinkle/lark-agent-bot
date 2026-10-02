package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtFromMime(t *testing.T) {
	cases := map[string]string{
		"image/jpeg": ".jpg",
		"image/gif":  ".gif",
		"image/webp": ".webp",
		"image/bmp":  ".bmp",
		"image/png":  ".png",
		"":           ".png", // unknown / missing MIME falls back to png
	}
	for mime, want := range cases {
		if got := ExtFromMime(mime); got != want {
			t.Errorf("ExtFromMime(%q) = %q, want %q", mime, got, want)
		}
	}
}

// Images arriving without a filename (Feishu sends bytes + MIME only) must
// still land on disk with a usable extension.
func TestSaveFilesToDisk_ImageWithoutFileName(t *testing.T) {
	dir := t.TempDir()
	saved := SaveFilesToDisk(dir, "msg1", []FileAttachment{
		{MimeType: "image/png", Data: []byte("\x89PNG fake"), FileName: "img_1_0" + ExtFromMime("image/png")},
	})
	if len(saved) != 1 {
		t.Fatalf("expected 1 saved file, got %d", len(saved))
	}
	if filepath.Ext(saved[0]) != ".png" {
		t.Errorf("saved image lost its extension: %q", saved[0])
	}
	if _, err := os.Stat(saved[0]); err != nil {
		t.Errorf("saved image not on disk: %v", err)
	}
}
