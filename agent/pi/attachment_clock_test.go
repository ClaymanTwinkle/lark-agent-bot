package pi

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestPiSessionAttachmentDirsUniqueWithFrozenClock(t *testing.T) {
	workDir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		seen := make(map[string]bool)
		for i := 0; i < 10; i++ {
			s, err := newPiSession(context.Background(), "pi", nil, workDir, "", "", "", false, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if seen[s.attachDir] {
				t.Fatalf("sessions created in the same clock tick share attachments: %s", s.attachDir)
			}
			seen[s.attachDir] = true
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
