package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// videoSendRecorder captures what a video send uploads and posts.
type videoSendRecorder struct {
	mu            sync.Mutex
	fileType      string
	fileName      string
	duration      string
	imageUploads  int
	msgType       string
	content       map[string]string
	imageUploadOK bool
}

func newVideoSendTestPlatform(t *testing.T, rec *videoSendRecorder, meta core.VideoMeta) *Platform {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rec.mu.Lock()
		defer rec.mu.Unlock()
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(t, w, map[string]any{
				"code": 0, "msg": "success", "expire": 7200, "tenant_access_token": "valid-token",
			})
		case "/open-apis/im/v1/files":
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				t.Errorf("parse upload form: %v", err)
			}
			rec.fileType = r.FormValue("file_type")
			rec.fileName = r.FormValue("file_name")
			rec.duration = r.FormValue("duration")
			writeJSON(t, w, map[string]any{
				"code": 0, "msg": "success", "data": map[string]any{"file_key": "file_v3_video"},
			})
		case "/open-apis/im/v1/images":
			rec.imageUploads++
			if !rec.imageUploadOK {
				writeJSON(t, w, map[string]any{"code": 234001, "msg": "invalid image"})
				return
			}
			writeJSON(t, w, map[string]any{
				"code": 0, "msg": "success", "data": map[string]any{"image_key": "img_v3_cover"},
			})
		case "/open-apis/im/v1/messages":
			var body struct {
				MsgType string `json:"msg_type"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode message body: %v", err)
			}
			rec.msgType = body.MsgType
			if err := json.Unmarshal([]byte(body.Content), &rec.content); err != nil {
				t.Errorf("message content is not JSON: %v", err)
			}
			writeJSON(t, w, map[string]any{
				"code": 0, "msg": "success", "data": map[string]any{"message_id": "om_video"},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	const appID, appSecret = "cli_video_send", "secret"
	return &Platform{
		platformName: "feishu",
		domain:       srv.URL,
		appID:        appID,
		appSecret:    appSecret,
		client: lark.NewClient(appID, appSecret,
			lark.WithOpenBaseUrl(srv.URL),
			lark.WithHttpClient(srv.Client()),
		),
		replayClient: lark.NewClient(appID, appSecret,
			lark.WithEnableTokenCache(false),
			lark.WithOpenBaseUrl(srv.URL),
			lark.WithHttpClient(srv.Client()),
		),
		probeVideo: func(context.Context, []byte, string) core.VideoMeta { return meta },
	}
}

// Regression: video bubbles showed 00:00 and no cover because the upload
// carried no duration and the message no image_key.
func TestSendVideo_SendsDurationAndCover(t *testing.T) {
	rec := &videoSendRecorder{imageUploadOK: true}
	p := newVideoSendTestPlatform(t, rec, core.VideoMeta{DurationMs: 12500, Cover: []byte{0xFF, 0xD8, 0xFF}})

	err := p.SendVideo(context.Background(), replyContext{chatID: "oc_chat"}, []byte("video-bytes"), "mp4", "demo.mp4")
	if err != nil {
		t.Fatalf("SendVideo() error = %v", err)
	}

	if rec.fileType != larkim.FileTypeMp4 || rec.fileName != "demo.mp4" {
		t.Fatalf("upload file_type=%q file_name=%q, want mp4 demo.mp4", rec.fileType, rec.fileName)
	}
	if rec.duration != "12500" {
		t.Fatalf("upload duration = %q, want 12500", rec.duration)
	}
	if rec.msgType != larkim.MsgTypeMedia {
		t.Fatalf("msg_type = %q, want media", rec.msgType)
	}
	if rec.content["file_key"] != "file_v3_video" || rec.content["image_key"] != "img_v3_cover" {
		t.Fatalf("content = %v, want file_key and cover image_key", rec.content)
	}
}

// `send --file clip.mp4` also renders as a video bubble, so it needs the
// same metadata as SendVideo.
func TestSendFile_MP4GetsDurationAndCover(t *testing.T) {
	rec := &videoSendRecorder{imageUploadOK: true}
	p := newVideoSendTestPlatform(t, rec, core.VideoMeta{DurationMs: 3000, Cover: []byte{0xFF, 0xD8, 0xFF}})

	err := p.SendFile(context.Background(), replyContext{chatID: "oc_chat"}, core.FileAttachment{
		MimeType: "video/mp4", FileName: "clip.mp4", Data: []byte("video-bytes"),
	})
	if err != nil {
		t.Fatalf("SendFile() error = %v", err)
	}
	if rec.duration != "3000" {
		t.Fatalf("upload duration = %q, want 3000", rec.duration)
	}
	if rec.msgType != larkim.MsgTypeMedia || rec.content["image_key"] != "img_v3_cover" {
		t.Fatalf("msg_type=%q content=%v, want media with cover", rec.msgType, rec.content)
	}
}

// A clip whose metadata can't be read (no ffmpeg, unknown container) is
// still delivered, just without duration or cover.
func TestSendVideo_WithoutMetadataStillSends(t *testing.T) {
	rec := &videoSendRecorder{imageUploadOK: true}
	p := newVideoSendTestPlatform(t, rec, core.VideoMeta{})

	err := p.SendVideo(context.Background(), replyContext{chatID: "oc_chat"}, []byte("video-bytes"), "webm", "")
	if err != nil {
		t.Fatalf("SendVideo() error = %v", err)
	}
	if rec.duration != "" {
		t.Fatalf("upload duration = %q, want none when unknown", rec.duration)
	}
	if rec.imageUploads != 0 {
		t.Fatalf("image uploads = %d, want 0 without a cover", rec.imageUploads)
	}
	if rec.fileName != "video.webm" {
		t.Fatalf("file_name = %q, want video.webm", rec.fileName)
	}
	if _, ok := rec.content["image_key"]; ok || rec.content["file_key"] != "file_v3_video" {
		t.Fatalf("content = %v, want file_key only", rec.content)
	}
}

// A failed cover upload must not block the video itself.
func TestSendVideo_CoverUploadFailureStillSends(t *testing.T) {
	rec := &videoSendRecorder{imageUploadOK: false}
	p := newVideoSendTestPlatform(t, rec, core.VideoMeta{DurationMs: 8000, Cover: []byte{0xFF, 0xD8, 0xFF}})

	err := p.SendVideo(context.Background(), replyContext{chatID: "oc_chat"}, []byte("video-bytes"), "mp4", "demo.mp4")
	if err != nil {
		t.Fatalf("SendVideo() error = %v", err)
	}
	if rec.duration != "8000" {
		t.Fatalf("upload duration = %q, want 8000", rec.duration)
	}
	if _, ok := rec.content["image_key"]; ok || rec.content["file_key"] != "file_v3_video" {
		t.Fatalf("content = %v, want file_key only after cover failure", rec.content)
	}
}
