package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mp4Box builds an ISO-BMFF box with a 32-bit size header.
func mp4Box(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	box := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(box[0:4], uint32(8+len(body)))
	copy(box[4:8], typ)
	return append(box, body...)
}

// mvhdV0 builds a version-0 mvhd payload (32-bit times and duration).
func mvhdV0(timescale, duration uint32) []byte {
	p := make([]byte, 100)
	binary.BigEndian.PutUint32(p[12:16], timescale)
	binary.BigEndian.PutUint32(p[16:20], duration)
	return p
}

// mvhdV1 builds a version-1 mvhd payload (64-bit times and duration).
func mvhdV1(timescale uint32, duration uint64) []byte {
	p := make([]byte, 112)
	p[0] = 1
	binary.BigEndian.PutUint32(p[20:24], timescale)
	binary.BigEndian.PutUint64(p[24:32], duration)
	return p
}

func TestMP4DurationMs(t *testing.T) {
	ftyp := mp4Box("ftyp", []byte("isom\x00\x00\x02\x00isomiso2avc1mp41"))
	mdat := mp4Box("mdat", make([]byte, 64))

	// A 64-bit-size mdat (size field == 1) ahead of moov.
	largeMdat := make([]byte, 16+32)
	binary.BigEndian.PutUint32(largeMdat[0:4], 1)
	copy(largeMdat[4:8], "mdat")
	binary.BigEndian.PutUint64(largeMdat[8:16], uint64(len(largeMdat)))

	tests := []struct {
		name string
		data []byte
		want int
	}{
		{
			name: "version 0 mvhd",
			data: bytes.Join([][]byte{ftyp, mp4Box("moov", mp4Box("mvhd", mvhdV0(1000, 5000))), mdat}, nil),
			want: 5000,
		},
		{
			name: "version 1 mvhd",
			data: bytes.Join([][]byte{ftyp, mp4Box("moov", mp4Box("mvhd", mvhdV1(90000, 90000*12+45000)))}, nil),
			want: 12500,
		},
		{
			name: "moov after mdat",
			data: bytes.Join([][]byte{ftyp, mdat, mp4Box("moov", mp4Box("mvhd", mvhdV0(600, 1500)))}, nil),
			want: 2500,
		},
		{
			name: "mvhd after another moov child",
			data: bytes.Join([][]byte{ftyp, mp4Box("moov", mp4Box("udta", []byte("meta")), mp4Box("mvhd", mvhdV0(1000, 750)))}, nil),
			want: 750,
		},
		{
			name: "64-bit box size before moov",
			data: bytes.Join([][]byte{ftyp, largeMdat, mp4Box("moov", mp4Box("mvhd", mvhdV0(1000, 3000)))}, nil),
			want: 3000,
		},
		{
			name: "no moov",
			data: bytes.Join([][]byte{ftyp, mdat}, nil),
			want: 0,
		},
		{
			name: "unknown duration",
			data: mp4Box("moov", mp4Box("mvhd", mvhdV0(1000, 0xFFFFFFFF))),
			want: 0,
		},
		{
			name: "zero timescale",
			data: mp4Box("moov", mp4Box("mvhd", mvhdV0(0, 5000))),
			want: 0,
		},
		{
			name: "truncated mvhd",
			data: mp4Box("moov", mp4Box("mvhd", []byte{0, 0, 0, 0, 1, 2})),
			want: 0,
		},
		{
			name: "box size past end of data",
			data: append([]byte{0, 0, 0x10, 0, 'm', 'o', 'o', 'v'}, make([]byte, 16)...),
			want: 0,
		},
		{
			name: "not an mp4",
			data: []byte("\x1aE\xdf\xa3 this is webm-ish garbage"),
			want: 0,
		},
		{
			name: "empty",
			data: nil,
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MP4DurationMs(tt.data); got != tt.want {
				t.Fatalf("MP4DurationMs() = %d, want %d", got, tt.want)
			}
		})
	}
}

// Without ffmpeg the cover is skipped but the mp4 duration is still read.
func TestProbeVideo_WithoutFFmpegStillReadsDuration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())

	video := mp4Box("moov", mp4Box("mvhd", mvhdV0(1000, 4200)))
	meta := ProbeVideo(context.Background(), video, ".mp4")
	if meta.DurationMs != 4200 {
		t.Fatalf("DurationMs = %d, want 4200", meta.DurationMs)
	}
	if meta.Cover != nil {
		t.Fatalf("Cover = %d bytes, want nil without ffmpeg", len(meta.Cover))
	}
}

func TestProbeVideo_WithFFmpeg(t *testing.T) {
	ffmpegPath, err := lookFFmpeg()
	if err != nil {
		t.Skip("ffmpeg not found")
	}

	// mp4 duration comes from the header; mkv has no mvhd, so its duration
	// has to come from ffmpeg's input summary.
	for _, ext := range []string{".mp4", ".mkv"} {
		t.Run(ext, func(t *testing.T) {
			clip := filepath.Join(t.TempDir(), "clip"+ext)
			gen := exec.Command(ffmpegPath, "-hide_banner", "-loglevel", "error",
				"-f", "lavfi", "-i", "testsrc=duration=3:size=320x240:rate=10",
				"-pix_fmt", "yuv420p", "-y", clip)
			if out, err := gen.CombinedOutput(); err != nil {
				t.Fatalf("generate test clip: %v\n%s", err, out)
			}
			video, err := os.ReadFile(clip)
			if err != nil {
				t.Fatal(err)
			}

			meta := ProbeVideo(context.Background(), video, ext)
			if meta.DurationMs < 2900 || meta.DurationMs > 3100 {
				t.Fatalf("DurationMs = %d, want ~3000", meta.DurationMs)
			}
			if !bytes.HasPrefix(meta.Cover, []byte{0xFF, 0xD8}) {
				t.Fatalf("Cover is not a JPEG (%d bytes)", len(meta.Cover))
			}
		})
	}
}

func TestParseFFmpegDurationMs(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   int
	}{
		{
			name: "mkv input summary",
			stderr: "Input #0, matroska,webm, from 'clip.mkv':\n" +
				"  Duration: 00:00:03.04, start: 0.000000, bitrate: 98 kb/s\n" +
				"  Stream #0:0: Video: h264\n",
			want: 3040,
		},
		{name: "hours and minutes", stderr: "  Duration: 01:02:03.50, start: 0", want: 3723500},
		{name: "not available", stderr: "  Duration: N/A, start: 0.000000, bitrate: N/A", want: 0},
		{name: "missing", stderr: "pipe:1: Invalid argument", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseFFmpegDurationMs(tt.stderr); got != tt.want {
				t.Fatalf("parseFFmpegDurationMs() = %d, want %d", got, tt.want)
			}
		})
	}
}

// writeStubTool creates an executable named name (plus .exe on Windows).
func writeStubTool(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The npm installer puts ffmpeg in ~/.lark-agent-bot/bin so package upgrades
// don't delete it; it must be found there when it isn't on PATH.
func TestLookTool_FindsHomeToolDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	want := writeStubTool(t, filepath.Join(home, ".lark-agent-bot", "bin"), "ffmpeg")

	got, err := lookTool("ffmpeg")
	if err != nil {
		t.Fatalf("lookTool() error = %v", err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("lookTool() = %q, want %q", got, want)
	}
	if _, err := lookTool("lark-agent-bot-no-such-tool"); err == nil {
		t.Fatal("lookTool() found a tool that isn't anywhere")
	}
}

func TestLookPathIn(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Base(writeStubTool(t, dir, "ffmpeg"))

	got, err := lookPathIn(dir, "ffmpeg")
	if err != nil {
		t.Fatalf("lookPathIn() error = %v", err)
	}
	if !strings.EqualFold(got, filepath.Join(dir, name)) {
		t.Fatalf("lookPathIn() = %q, want %q", got, filepath.Join(dir, name))
	}
	if _, err := lookPathIn(dir, "ffprobe"); err == nil {
		t.Fatal("lookPathIn() found a tool that isn't there")
	}
}

func TestCoverOffset(t *testing.T) {
	if got := coverOffset(0); got != 0 {
		t.Fatalf("coverOffset(0) = %v, want 0", got)
	}
	if got := coverOffset(1500); got != 0 {
		t.Fatalf("coverOffset(1500) = %v, want 0", got)
	}
	if got := coverOffset(10000); got == 0 {
		t.Fatal("coverOffset(10000) = 0, want a later frame for long clips")
	}
}
