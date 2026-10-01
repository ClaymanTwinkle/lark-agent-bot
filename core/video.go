package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// VideoMeta is the metadata a platform needs to render an outbound clip as
// a proper video bubble: a duration label and a cover frame.
type VideoMeta struct {
	DurationMs int    // 0 when unknown
	Cover      []byte // JPEG frame; nil when ffmpeg is unavailable or extraction failed
}

// videoProbeTimeout bounds each ffmpeg run so a pathological input can't
// stall an outbound send.
const videoProbeTimeout = 30 * time.Second

// ffmpegStderrTail caps how much ffmpeg stderr goes into an error message.
const ffmpegStderrTail = 512

var warnNoFFmpegForVideoOnce sync.Once

// ProbeVideo collects the duration and a cover frame for an outbound video.
// It never fails: whatever can't be determined is left zero and the reason
// is logged, so callers can still send the clip without it.
//
// Duration for mp4/mov/m4v is read from the container header in pure Go, so
// it works without ffmpeg. Other containers take it from ffmpeg's input
// summary. The cover frame always needs ffmpeg. ext is the file extension
// (e.g. ".mp4"), used only to name the temp file ffmpeg reads from.
func ProbeVideo(ctx context.Context, video []byte, ext string) VideoMeta {
	meta := VideoMeta{DurationMs: MP4DurationMs(video)}

	ffmpegPath, err := lookFFmpeg()
	if err != nil {
		warnNoFFmpegForVideoOnce.Do(func() {
			slog.Warn("video: ffmpeg not found, videos are sent without a cover frame")
		})
		return meta
	}

	// ffmpeg can't read an mp4 whose moov atom sits at the end from a pipe
	// (it has to seek), so hand it a real file.
	tmp, err := os.CreateTemp("", "lark-agent-bot-video-*"+ext)
	if err != nil {
		slog.Warn("video: create temp file failed", "error", err)
		return meta
	}
	path := tmp.Name()
	defer func() { _ = os.Remove(path) }()
	_, werr := tmp.Write(video)
	// Close before ffmpeg opens it: Windows won't share an open handle.
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		slog.Warn("video: write temp file failed", "error", werr)
		return meta
	}

	cover, durationMs, err := extractVideoFrame(ctx, ffmpegPath, path, coverOffset(meta.DurationMs))
	if meta.DurationMs == 0 {
		meta.DurationMs = durationMs
	}
	if err != nil {
		slog.Warn("video: extract cover frame failed", "error", err)
	} else {
		meta.Cover = cover
	}
	return meta
}

// coverOffset picks where to grab the cover frame. The very first frame is
// often black (fade-ins), so clips long enough get a frame at 1s.
func coverOffset(durationMs int) time.Duration {
	if durationMs >= 2000 {
		return time.Second
	}
	return 0
}

// extractVideoFrame grabs one JPEG frame at offset. It also returns the
// input duration ffmpeg reports (0 if absent), even when no frame came out.
func extractVideoFrame(ctx context.Context, ffmpegPath, path string, offset time.Duration) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, videoProbeTimeout)
	defer cancel()

	// Log level info keeps the input summary that carries "Duration: ...".
	args := []string{"-hide_banner", "-nostats", "-loglevel", "info"}
	if offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(offset.Seconds(), 'f', 3, 64))
	}
	args = append(args,
		"-i", path,
		"-frames:v", "1",
		"-an",
		"-f", "image2pipe",
		"-c:v", "mjpeg",
		"-q:v", "3",
		"pipe:1",
	)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	durationMs := parseFFmpegDurationMs(stderr.String())
	if runErr != nil {
		return nil, durationMs, fmt.Errorf("ffmpeg frame extraction failed: %w (stderr: %s)", runErr, stderrTail(stderr.String()))
	}
	if stdout.Len() == 0 {
		return nil, durationMs, fmt.Errorf("ffmpeg produced no frame (stderr: %s)", stderrTail(stderr.String()))
	}
	return stdout.Bytes(), durationMs, nil
}

var ffmpegDurationRe = regexp.MustCompile(`Duration: (\d+):(\d{2}):(\d{2}(?:\.\d+)?)`)

// parseFFmpegDurationMs reads "Duration: HH:MM:SS.ss" from ffmpeg's input
// summary. It returns 0 when the line is missing or says "N/A".
func parseFFmpegDurationMs(stderr string) int {
	m := ffmpegDurationRe.FindStringSubmatch(stderr)
	if m == nil {
		return 0
	}
	hours, err1 := strconv.Atoi(m[1])
	minutes, err2 := strconv.Atoi(m[2])
	seconds, err3 := strconv.ParseFloat(m[3], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0
	}
	total := float64(hours)*3600 + float64(minutes)*60 + seconds
	if total <= 0 || total > math.MaxInt32/1000 {
		return 0
	}
	return int(math.Round(total * 1000))
}

func stderrTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > ffmpegStderrTail {
		s = "..." + s[len(s)-ffmpegStderrTail:]
	}
	return s
}

// MP4DurationMs reads the movie duration from the moov/mvhd box of an
// ISO-BMFF file (mp4, mov, m4v). It returns 0 when the data isn't such a
// file or the header carries no duration (e.g. fragmented mp4).
func MP4DurationMs(data []byte) int {
	moov, ok := findMP4Box(data, "moov")
	if !ok {
		return 0
	}
	mvhd, ok := findMP4Box(moov, "mvhd")
	if !ok || len(mvhd) < 4 {
		return 0
	}

	var timescale, duration uint64
	switch mvhd[0] { // version; the 3 flag bytes follow
	case 0:
		// creation(4) modification(4) timescale(4) duration(4)
		if len(mvhd) < 20 {
			return 0
		}
		timescale = uint64(binary.BigEndian.Uint32(mvhd[12:16]))
		d := binary.BigEndian.Uint32(mvhd[16:20])
		if d == math.MaxUint32 {
			return 0
		}
		duration = uint64(d)
	case 1:
		// creation(8) modification(8) timescale(4) duration(8)
		if len(mvhd) < 32 {
			return 0
		}
		timescale = uint64(binary.BigEndian.Uint32(mvhd[20:24]))
		duration = binary.BigEndian.Uint64(mvhd[24:32])
		if duration == math.MaxUint64 {
			return 0
		}
	default:
		return 0
	}
	if timescale == 0 {
		return 0
	}
	secs := duration / timescale
	if secs > math.MaxInt32/1000 {
		return 0
	}
	return int(secs*1000 + duration%timescale*1000/timescale)
}

// findMP4Box returns the payload of the first box of the given type among
// the sibling boxes in data.
func findMP4Box(data []byte, boxType string) ([]byte, bool) {
	for len(data) >= 8 {
		size := uint64(binary.BigEndian.Uint32(data[0:4]))
		header := uint64(8)
		switch size {
		case 0: // box extends to the end of the data
			size = uint64(len(data))
		case 1: // 64-bit size follows the type
			if len(data) < 16 {
				return nil, false
			}
			size = binary.BigEndian.Uint64(data[8:16])
			header = 16
		}
		if size < header || size > uint64(len(data)) {
			return nil, false
		}
		if string(data[4:8]) == boxType {
			return data[header:size], true
		}
		data = data[size:]
	}
	return nil, false
}
