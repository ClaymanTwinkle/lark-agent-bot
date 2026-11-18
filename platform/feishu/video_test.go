package feishu

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

var (
	testPNG = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	testMP4 = append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0}, []byte("fake mp4 body")...)
)

func TestDetectVideoMimeType(t *testing.T) {
	ftyp := func(brand string) []byte {
		return append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p'}, []byte(brand+"\x00\x00\x00\x00")...)
	}
	ebml := func(docType string) []byte {
		return append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x82, 0x84}, []byte(docType)...)
	}
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"mp4 isom", ftyp("isom"), "video/mp4"},
		{"mp4 mp42", ftyp("mp42"), "video/mp4"},
		{"3gp", ftyp("3gp4"), "video/mp4"},
		{"quicktime", ftyp("qt  "), "video/quicktime"},
		{"heic image", ftyp("heic"), ""},
		{"avif image", ftyp("avif"), ""},
		{"m4a audio", ftyp("M4A "), ""},
		{"webm", ebml("webm"), "video/webm"},
		{"matroska", ebml("matroska"), "video/x-matroska"},
		{"avi", []byte("RIFF\x00\x00\x00\x00AVI LIST"), "video/x-msvideo"},
		{"webp image", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), ""},
		{"png image", testPNG, ""},
		{"too short", []byte{0, 0, 0}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectVideoMimeType(c.data); got != c.want {
				t.Fatalf("detectVideoMimeType = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDetectMimeTypeRecognizesVideo(t *testing.T) {
	if got := detectMimeType(testMP4); got != "video/mp4" {
		t.Fatalf("detectMimeType(mp4) = %q, want video/mp4", got)
	}
	if got := detectMimeType(testPNG); got != "image/png" {
		t.Fatalf("detectMimeType(png) = %q, want image/png", got)
	}
}

func TestVideoLabel(t *testing.T) {
	cases := []struct {
		name       string
		fileName   string
		durationMs int
		want       string
	}{
		{"name and duration", "demo.mp4", 12345, "video: demo.mp4, 12s"},
		{"no name", "", 3000, "video, 3s"},
		{"no duration", "demo.mp4", 0, "video: demo.mp4"},
		{"nothing", "", 0, "video"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := videoLabel(c.fileName, c.durationMs); got != c.want {
				t.Fatalf("videoLabel = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNewDownloadedFile(t *testing.T) {
	named := newDownloadedFile(testMP4, "demo.mp4", "om_1")
	if named.FileName != "demo.mp4" || named.MimeType != "video/mp4" {
		t.Fatalf("named video = %q %q, want demo.mp4 video/mp4", named.FileName, named.MimeType)
	}
	nameless := newDownloadedFile(testMP4, "", "om_1")
	if nameless.FileName != "video_om_1.mp4" {
		t.Fatalf("nameless video FileName = %q, want video_om_1.mp4", nameless.FileName)
	}
	// Non-video files keep an empty name so SaveFilesToDisk picks its own.
	other := newDownloadedFile([]byte("%PDF-1.4 body"), "", "om_1")
	if other.FileName != "" {
		t.Fatalf("nameless non-video FileName = %q, want empty", other.FileName)
	}
}

// videoServer serves a cover image and a video for message msgID. When
// oversize is true the video advertises a size above the platform's cap,
// so the download fails the way a too-large clip would.
func videoServer(t *testing.T, msgID, imageKey, fileKey string, oversize bool) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path+"?type="+r.URL.Query().Get("type"))
		mu.Unlock()
		base := "/open-apis/im/v1/messages/" + msgID + "/resources/"
		switch r.URL.Path {
		case base + imageKey:
			_, _ = w.Write(testPNG)
		case base + fileKey:
			if oversize {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", 100<<20))
				w.Header().Set("Content-Length", "1")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte{0})
				return
			}
			_, _ = w.Write(testMP4)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(requests)
	}
}

func dispatchVideo(t *testing.T, p *Platform, msgID, content string) *core.Message {
	t.Helper()
	got := make(chan *core.Message, 1)
	p.handler = func(_ core.Platform, msg *core.Message) { got <- msg }
	p.dispatchMessage(context.Background(), "media", content, nil, msgID,
		"feishu:ou_user:ou_user", "", "",
		replyContext{messageID: msgID, sessionKey: "feishu:ou_user:ou_user"}, "", 0)
	select {
	case msg := <-got:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for dispatched message")
		return nil
	}
}

func TestDispatchMessageMediaAttachesVideo(t *testing.T) {
	const msgID = "om_video"
	srv, requests := videoServer(t, msgID, "img_cover", "file_video", false)
	p := newTestPlatformForDownload(srv.URL, 1<<20, 10<<20)

	msg := dispatchVideo(t, p, msgID,
		`{"file_key":"file_video","image_key":"img_cover","file_name":"demo.mp4","duration":12345}`)

	if msg.Content != "[video: demo.mp4, 12s]" {
		t.Fatalf("Content = %q, want [video: demo.mp4, 12s]", msg.Content)
	}
	if len(msg.Images) != 1 || !bytes.Equal(msg.Images[0].Data, testPNG) {
		t.Fatalf("Images = %+v, want the cover image", msg.Images)
	}
	if len(msg.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(msg.Files))
	}
	f := msg.Files[0]
	if f.FileName != "demo.mp4" || f.MimeType != "video/mp4" || !bytes.Equal(f.Data, testMP4) {
		t.Fatalf("Files[0] = {%q %q %d bytes}, want demo.mp4 video/mp4 with the served body", f.FileName, f.MimeType, len(f.Data))
	}
	wantVideoReq := "/open-apis/im/v1/messages/" + msgID + "/resources/file_video?type=file"
	if !slices.Contains(requests(), wantVideoReq) {
		t.Fatalf("requests = %v, want one for %s", requests(), wantVideoReq)
	}
}

func TestDispatchMessageMediaWithoutFileNameGetsVideoName(t *testing.T) {
	const msgID = "om_video_noname"
	srv, _ := videoServer(t, msgID, "img_cover", "file_video", false)
	p := newTestPlatformForDownload(srv.URL, 1<<20, 10<<20)

	msg := dispatchVideo(t, p, msgID, `{"file_key":"file_video","image_key":"img_cover","duration":3000}`)

	if msg.Content != "[video, 3s]" {
		t.Fatalf("Content = %q, want [video, 3s]", msg.Content)
	}
	if len(msg.Files) != 1 || msg.Files[0].FileName != "video_"+msgID+".mp4" {
		t.Fatalf("Files = %+v, want one file named video_%s.mp4", msg.Files, msgID)
	}
}

func TestDispatchMessageMediaDownloadFailureKeepsCover(t *testing.T) {
	const msgID = "om_video_big"
	srv, _ := videoServer(t, msgID, "img_cover", "file_video", true)
	p := newTestPlatformForDownload(srv.URL, 1<<20, 10<<20)

	msg := dispatchVideo(t, p, msgID,
		`{"file_key":"file_video","image_key":"img_cover","file_name":"big.mp4","duration":60000}`)

	if msg.Content != "[video: big.mp4, 60s - download failed]" {
		t.Fatalf("Content = %q, want the download-failed label", msg.Content)
	}
	if len(msg.Files) != 0 {
		t.Fatalf("len(Files) = %d, want 0 after a failed download", len(msg.Files))
	}
	if len(msg.Images) != 1 {
		t.Fatalf("len(Images) = %d, want the cover image", len(msg.Images))
	}
}

func TestFormatMergeForwardTreeAttachesVideo(t *testing.T) {
	const parentID = "om_forward"
	const childID = "om_forward_child"
	srv, _ := videoServer(t, childID, "img_cover", "file_video", false)
	p := newTestPlatformForDownload(srv.URL, 1<<20, 10<<20)

	child := &larkim.Message{
		MessageId: strPtr(childID),
		MsgType:   strPtr("media"),
		Sender:    &larkim.Sender{Id: strPtr("ou_alice")},
		Body: &larkim.MessageBody{Content: strPtr(
			`{"file_key":"file_video","image_key":"img_cover","file_name":"clip.mp4","duration":5000}`)},
	}
	var sb strings.Builder
	var images []core.ImageAttachment
	var files []core.FileAttachment
	p.formatMergeForwardTree(parentID, map[string][]*larkim.Message{parentID: {child}},
		map[string]string{"ou_alice": "Alice"}, &sb, &images, &files, 0)

	if !strings.Contains(sb.String(), "Alice: [video: clip.mp4, 5s]") {
		t.Fatalf("tree text = %q, want the video label", sb.String())
	}
	if len(files) != 1 || files[0].FileName != "clip.mp4" || !bytes.Equal(files[0].Data, testMP4) {
		t.Fatalf("files = %+v, want clip.mp4 with the served body", files)
	}
	if len(images) != 0 {
		t.Fatalf("len(images) = %d, want 0 (no cover for forwarded videos)", len(images))
	}
}

// postWithVideo is a post as Feishu sends it when a video is pasted into a
// rich-text message: the video is a "media" element with no name.
const postWithVideo = `{"title":"","content":[[{"tag":"text","text":"我发视频给你看看"}],` +
	`[{"tag":"media","file_key":"file_video","image_key":"img_cover"}]]}`

func TestPostVideos(t *testing.T) {
	got := postVideos(postWithVideo)
	if len(got) != 1 || got[0].FileKey != "file_video" || got[0].ImageKey != "img_cover" {
		t.Fatalf("postVideos(flat) = %+v, want one video file_video/img_cover", got)
	}
	langKeyed := `{"zh_cn":{"title":"t","content":[[{"tag":"media","file_key":"fk"}]]}}`
	if got := postVideos(langKeyed); len(got) != 1 || got[0].FileKey != "fk" {
		t.Fatalf("postVideos(lang-keyed) = %+v, want one video fk", got)
	}
	noKey := `{"content":[[{"tag":"media","image_key":"img"},{"tag":"text","text":"x"}]]}`
	if got := postVideos(noKey); len(got) != 0 {
		t.Fatalf("postVideos(no file_key) = %+v, want none", got)
	}
	if got := postVideos("not json"); got != nil {
		t.Fatalf("postVideos(invalid) = %+v, want nil", got)
	}
}

func TestDispatchMessagePostAttachesEmbeddedVideo(t *testing.T) {
	const msgID = "om_post_video"
	srv, _ := videoServer(t, msgID, "img_cover", "file_video", false)
	p := newTestPlatformForDownload(srv.URL, 1<<20, 10<<20)

	got := make(chan *core.Message, 1)
	p.handler = func(_ core.Platform, msg *core.Message) { got <- msg }
	p.dispatchMessage(context.Background(), "post", postWithVideo, nil, msgID,
		"feishu:ou_user:ou_user", "", "",
		replyContext{messageID: msgID, sessionKey: "feishu:ou_user:ou_user"}, "", 0)

	var msg *core.Message
	select {
	case msg = <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for dispatched message")
	}
	if msg.Content != "我发视频给你看看\n[video]" {
		t.Fatalf("Content = %q, want the text and a [video] marker", msg.Content)
	}
	if len(msg.Images) != 1 || !bytes.Equal(msg.Images[0].Data, testPNG) {
		t.Fatalf("Images = %d, want the video cover", len(msg.Images))
	}
	if len(msg.Files) != 1 || msg.Files[0].FileName != "video_"+msgID+".mp4" || !bytes.Equal(msg.Files[0].Data, testMP4) {
		t.Fatalf("Files = %+v, want video_%s.mp4 with the served body", msg.Files, msgID)
	}
}

func TestDispatchMessagePostVideoDownloadFailure(t *testing.T) {
	const msgID = "om_post_video_big"
	srv, _ := videoServer(t, msgID, "img_cover", "file_video", true)
	p := newTestPlatformForDownload(srv.URL, 1<<20, 10<<20)

	got := make(chan *core.Message, 1)
	p.handler = func(_ core.Platform, msg *core.Message) { got <- msg }
	p.dispatchMessage(context.Background(), "post", postWithVideo, nil, msgID,
		"feishu:ou_user:ou_user", "", "",
		replyContext{messageID: msgID, sessionKey: "feishu:ou_user:ou_user"}, "", 0)

	var msg *core.Message
	select {
	case msg = <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for dispatched message")
	}
	if !strings.HasSuffix(msg.Content, "[video - download failed]") {
		t.Fatalf("Content = %q, want a download-failed marker", msg.Content)
	}
	if len(msg.Files) != 0 || len(msg.Images) != 1 {
		t.Fatalf("Files=%d Images=%d, want 0 files and the cover", len(msg.Files), len(msg.Images))
	}
}

func TestFormatMergeForwardTreeAttachesPostVideo(t *testing.T) {
	const parentID = "om_forward_post"
	const childID = "om_forward_post_child"
	srv, _ := videoServer(t, childID, "img_cover", "file_video", false)
	p := newTestPlatformForDownload(srv.URL, 1<<20, 10<<20)

	child := &larkim.Message{
		MessageId: strPtr(childID),
		MsgType:   strPtr("post"),
		Sender:    &larkim.Sender{Id: strPtr("ou_alice")},
		Body:      &larkim.MessageBody{Content: strPtr(postWithVideo)},
	}
	var sb strings.Builder
	var images []core.ImageAttachment
	var files []core.FileAttachment
	p.formatMergeForwardTree(parentID, map[string][]*larkim.Message{parentID: {child}},
		map[string]string{"ou_alice": "Alice"}, &sb, &images, &files, 0)

	if !strings.Contains(sb.String(), "[video]") {
		t.Fatalf("tree text = %q, want a [video] marker", sb.String())
	}
	if len(files) != 1 || files[0].FileName != "video_"+childID+".mp4" {
		t.Fatalf("files = %+v, want video_%s.mp4", files, childID)
	}
}

// A quoted post keeps its video as metadata only: the bytes are fetched
// later, and only when the quoted-file gates allow it.
func TestFetchSingleMessagePostVideoIsLazy(t *testing.T) {
	const msgID = "om_quoted_post"
	var resourceCalls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "expire": 7200, "tenant_access_token": "tok"})
		case r.URL.Path == "/open-apis/im/v1/messages/"+msgID:
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{
				"items": []map[string]any{{
					"msg_type": "post",
					"sender":   map[string]any{"id": "ou_alice", "sender_type": "user"},
					"body":     map[string]any{"content": postWithVideo},
				}},
			}})
		case strings.Contains(r.URL.Path, "/resources/"):
			mu.Lock()
			resourceCalls++
			mu.Unlock()
			_, _ = w.Write(testPNG)
		default:
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success"})
		}
	}))
	t.Cleanup(srv.Close)
	p := newTestPlatformForDownload(srv.URL, 1<<20, 10<<20)
	p.client = lark.NewClient("cli_test", "secret", lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client()))

	cm := p.fetchSingleMessage(context.Background(), msgID)
	if cm == nil {
		t.Fatal("fetchSingleMessage returned nil")
	}
	if len(cm.files) != 1 || cm.files[0].fileKey != "file_video" || cm.files[0].senderID != "ou_alice" || cm.files[0].messageID != msgID {
		t.Fatalf("files = %+v, want one lazy video entry from ou_alice", cm.files)
	}
	mu.Lock()
	defer mu.Unlock()
	if resourceCalls != 0 {
		t.Fatalf("resource calls = %d, want 0 (video must not be fetched eagerly)", resourceCalls)
	}
}
