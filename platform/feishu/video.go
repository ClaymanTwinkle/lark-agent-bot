package feishu

import (
	"bytes"
	"fmt"
	"log/slog"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// videoBody is the content of a Feishu "media" (video) message. FileKey
// points at the video itself, ImageKey at its cover image, and Duration is
// in milliseconds. FileName may be empty for clips recorded in the app.
type videoBody struct {
	FileKey  string `json:"file_key"`
	ImageKey string `json:"image_key"`
	FileName string `json:"file_name"`
	Duration int    `json:"duration"`
}

// videoLabel describes a video for the agent prompt, e.g.
// "video: demo.mp4, 12s". Callers wrap it in brackets and may append a
// download status.
func videoLabel(fileName string, durationMs int) string {
	label := "video"
	if fileName != "" {
		label += ": " + fileName
	}
	if durationMs > 0 {
		label += fmt.Sprintf(", %ds", durationMs/1000)
	}
	return label
}

// collectVideo downloads a video message's cover image and the video file
// itself. The returned text is the bracketed label the agent sees; it says
// "download failed" when the video could not be fetched, in which case only
// the cover image (if any) is attached.
func (p *Platform) collectVideo(messageID string, body videoBody) (string, []core.ImageAttachment, []core.FileAttachment) {
	var images []core.ImageAttachment
	if body.ImageKey != "" {
		if data, mimeType, err := p.downloadImage(messageID, body.ImageKey); err == nil {
			images = append(images, core.ImageAttachment{MimeType: mimeType, Data: data})
		} else {
			slog.Warn(p.tag()+": download video cover failed", "message_id", messageID, "error", err)
		}
	}

	label := videoLabel(body.FileName, body.Duration)
	if body.FileKey == "" {
		return "[" + label + "]", images, nil
	}
	data, err := p.downloadResource(messageID, body.FileKey, "file")
	if err != nil {
		slog.Error(p.tag()+": download video failed",
			"message_id", messageID, "file_key", body.FileKey, "file_name", body.FileName, "error", err)
		return "[" + label + " - download failed]", images, nil
	}
	return "[" + label + "]", images, []core.FileAttachment{newDownloadedFile(data, body.FileName, messageID)}
}

// newDownloadedFile wraps bytes downloaded from a Feishu message resource.
// A video without a file name gets "video_<messageID>.<ext>": otherwise
// core.SaveFilesToDisk would store it as an extension-less "file_<n>", and
// the agent could not tell it is a video.
func newDownloadedFile(data []byte, fileName, messageID string) core.FileAttachment {
	mimeType := detectMimeType(data)
	if fileName == "" {
		if ext := videoExtension(mimeType); ext != "" {
			fileName = "video_" + messageID + ext
		}
	}
	return core.FileAttachment{MimeType: mimeType, Data: data, FileName: fileName}
}

// videoExtension maps a MIME type from detectVideoMimeType to a file
// extension, or returns "" for anything else.
func videoExtension(mimeType string) string {
	switch mimeType {
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "video/x-matroska":
		return ".mkv"
	case "video/x-msvideo":
		return ".avi"
	}
	return ""
}

// detectVideoMimeType recognizes common video containers by their leading
// bytes and returns "" for anything else.
func detectVideoMimeType(data []byte) string {
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		// ISO base media file (MP4, MOV, 3GP). HEIF/AVIF images share the
		// container, so their brands must not be reported as video.
		switch string(data[8:12]) {
		case "qt  ":
			return "video/quicktime"
		case "heic", "heix", "heim", "heis", "hevc", "hevx", "mif1", "msf1", "avif", "avis":
			return ""
		case "M4A ", "M4B ", "M4P ":
			return ""
		}
		return "video/mp4"
	}
	if len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		// Matroska / WebM: the DocType element names which one it is.
		head := data[:min(len(data), 64)]
		if bytes.Contains(head, []byte("webm")) {
			return "video/webm"
		}
		return "video/x-matroska"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "AVI " {
		return "video/x-msvideo"
	}
	return ""
}
