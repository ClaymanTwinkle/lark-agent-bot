package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"hash/adler32"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	testDriveRoot  = "fldRootToken"
	testDriveFile  = "boxDriveFile"
	testDriveURL   = "https://example.feishu.cn/file/boxDriveFile"
	testUploadID   = "upload-1"
	testDriveChat  = "oc_drive_chat"
	testDriveOwner = "ou_drive_user"
)

type drivePart struct {
	uploadID string
	seq      int
	size     int
	checksum string
	data     []byte
}

type driveMember struct {
	docType    string
	memberType string
	memberID   string
	perm       string
	kind       string
}

// driveRecorder is a fake Feishu server for the cloud-space upload path. It
// records each call and answers like the real API.
type driveRecorder struct {
	mu        sync.Mutex
	blockSize int
	// rejectMembers lists member types the permission API refuses.
	rejectMembers map[string]bool

	rootCalls   int
	prepare     map[string]any
	parts       []drivePart
	finish      map[string]any
	members     []driveMember
	metaRequest map[string]any
	deleted     []string
	imUploads   []string // file_name of each im/v1/files upload
	imageUpload int
	msgType     string
	content     string
}

func newDriveTestPlatform(t *testing.T, rec *driveRecorder) *Platform {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rec.mu.Lock()
		defer rec.mu.Unlock()
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "expire": 7200, "tenant_access_token": "valid-token"})
		case r.URL.Path == "/open-apis/drive/explorer/v2/root_folder/meta":
			rec.rootCalls++
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{"token": testDriveRoot}})
		case r.URL.Path == "/open-apis/drive/v1/files/upload_prepare":
			rec.prepare = decodeBody(t, r)
			size := int(rec.prepare["size"].(float64))
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{
				"upload_id": testUploadID, "block_size": rec.blockSize, "block_num": (size + rec.blockSize - 1) / rec.blockSize,
			}})
		case r.URL.Path == "/open-apis/drive/v1/files/upload_part":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse upload_part form: %v", err)
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("upload_part file: %v", err)
				return
			}
			data, _ := io.ReadAll(f)
			seq, _ := strconv.Atoi(r.FormValue("seq"))
			size, _ := strconv.Atoi(r.FormValue("size"))
			rec.parts = append(rec.parts, drivePart{
				uploadID: r.FormValue("upload_id"), seq: seq, size: size, checksum: r.FormValue("checksum"), data: data,
			})
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{}})
		case r.URL.Path == "/open-apis/drive/v1/files/upload_finish":
			rec.finish = decodeBody(t, r)
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{"file_token": testDriveFile}})
		case r.URL.Path == "/open-apis/drive/v1/permissions/"+testDriveFile+"/members":
			body := decodeBody(t, r)
			m := driveMember{docType: r.URL.Query().Get("type")}
			m.memberType, _ = body["member_type"].(string)
			m.memberID, _ = body["member_id"].(string)
			m.perm, _ = body["perm"].(string)
			m.kind, _ = body["type"].(string)
			rec.members = append(rec.members, m)
			if rec.rejectMembers[m.memberType] {
				writeJSON(t, w, map[string]any{"code": 1063001, "msg": "Invalid parameter"})
				return
			}
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{"member": body}})
		case r.URL.Path == "/open-apis/drive/v1/metas/batch_query":
			rec.metaRequest = decodeBody(t, r)
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{
				"metas": []any{map[string]any{"doc_token": testDriveFile, "doc_type": "file", "url": testDriveURL}},
			}})
		case r.URL.Path == "/open-apis/drive/v1/files/"+testDriveFile && r.Method == http.MethodDelete:
			rec.deleted = append(rec.deleted, testDriveFile+"?type="+r.URL.Query().Get("type"))
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{}})
		case r.URL.Path == "/open-apis/im/v1/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse im upload form: %v", err)
			}
			rec.imUploads = append(rec.imUploads, r.FormValue("file_name"))
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{"file_key": "file_v3_small"}})
		case r.URL.Path == "/open-apis/im/v1/images":
			rec.imageUpload++
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{"image_key": "img_v3_small"}})
		case r.URL.Path == "/open-apis/im/v1/messages":
			var body struct {
				MsgType string `json:"msg_type"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode message body: %v", err)
			}
			rec.msgType, rec.content = body.MsgType, body.Content
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{"message_id": "om_sent"}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeJSON(t, w, map[string]any{"code": 404, "msg": "not found"})
		}
	}))
	t.Cleanup(srv.Close)

	const appID, appSecret = "cli_drive_send", "secret"
	return &Platform{
		platformName: "feishu",
		domain:       srv.URL,
		appID:        appID,
		appSecret:    appSecret,
		client:       lark.NewClient(appID, appSecret, lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client())),
		replayClient: lark.NewClient(appID, appSecret,
			lark.WithEnableTokenCache(false), lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client())),
		probeVideo: func(context.Context, []byte, string) core.VideoMeta { return core.VideoMeta{} },
	}
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decode %s body: %v", r.URL.Path, err)
	}
	return body
}

// A file over the file-message limit is uploaded to the cloud space in
// Feishu-sized blocks, shared with the chat, and sent as a link to it.
func TestSendFile_OverLimitSendsCloudSpaceLink(t *testing.T) {
	rec := &driveRecorder{blockSize: 10}
	p := newDriveTestPlatform(t, rec)
	p.imFileMaxBytes = 20
	data := []byte("abcdefghijklmnopqrstuvwxy") // 25 bytes: blocks of 10, 10, 5
	rc := replyContext{chatID: testDriveChat, sessionKey: "feishu:" + testDriveChat + ":root:om_root"}

	err := p.SendFile(context.Background(), rc, core.FileAttachment{FileName: "report.zip", Data: data})
	if err != nil {
		t.Fatalf("SendFile() error = %v", err)
	}

	if len(rec.imUploads) != 0 {
		t.Fatalf("im/v1/files uploads = %v, want none", rec.imUploads)
	}
	if got := rec.prepare; got["file_name"] != "report.zip" || got["parent_type"] != "explorer" ||
		got["parent_node"] != testDriveRoot || got["size"] != float64(len(data)) {
		t.Fatalf("upload_prepare body = %v", got)
	}
	var joined []byte
	for i, part := range rec.parts {
		if part.uploadID != testUploadID || part.seq != i || part.size != len(part.data) {
			t.Fatalf("part %d = upload_id %q seq %d size %d (%d bytes)", i, part.uploadID, part.seq, part.size, len(part.data))
		}
		if want := strconv.FormatUint(uint64(adler32.Checksum(part.data)), 10); part.checksum != want {
			t.Fatalf("part %d checksum = %q, want %q", i, part.checksum, want)
		}
		joined = append(joined, part.data...)
	}
	if len(rec.parts) != 3 || !bytes.Equal(joined, data) {
		t.Fatalf("uploaded %d parts with %q, want 3 parts with %q", len(rec.parts), joined, data)
	}
	if rec.finish["upload_id"] != testUploadID || rec.finish["block_num"] != float64(3) {
		t.Fatalf("upload_finish body = %v", rec.finish)
	}
	wantMember := driveMember{docType: "file", memberType: "openchat", memberID: testDriveChat, perm: "view", kind: "chat"}
	if len(rec.members) != 1 || rec.members[0] != wantMember {
		t.Fatalf("permission members = %+v, want [%+v]", rec.members, wantMember)
	}
	if rec.metaRequest["with_url"] != true {
		t.Fatalf("metas batch_query body = %v, want with_url", rec.metaRequest)
	}
	if rec.msgType != larkim.MsgTypePost {
		t.Fatalf("msg_type = %q, want post", rec.msgType)
	}
	for _, want := range []string{`"href":"` + testDriveURL + `"`, `"text":"report.zip"`, `(25 B)`} {
		if !strings.Contains(rec.content, want) {
			t.Fatalf("message content %s lacks %s", rec.content, want)
		}
	}
	if len(rec.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", rec.deleted)
	}

	// The root folder is looked up once.
	if err := p.SendFile(context.Background(), rc, core.FileAttachment{FileName: "again.zip", Data: data}); err != nil {
		t.Fatalf("second SendFile() error = %v", err)
	}
	if rec.rootCalls != 1 {
		t.Fatalf("root folder lookups = %d, want 1", rec.rootCalls)
	}
}

// Files at the limit still go out as file messages.
func TestSendFile_AtLimitStaysFileMessage(t *testing.T) {
	rec := &driveRecorder{blockSize: 10}
	p := newDriveTestPlatform(t, rec)
	p.imFileMaxBytes = 25

	err := p.SendFile(context.Background(), replyContext{chatID: testDriveChat}, core.FileAttachment{
		FileName: "notes.txt", Data: []byte("abcdefghijklmnopqrstuvwxy"),
	})
	if err != nil {
		t.Fatalf("SendFile() error = %v", err)
	}
	if len(rec.imUploads) != 1 || rec.prepare != nil {
		t.Fatalf("im uploads = %v, drive prepare = %v; want one im upload and no drive upload", rec.imUploads, rec.prepare)
	}
	if rec.msgType != larkim.MsgTypeFile {
		t.Fatalf("msg_type = %q, want file", rec.msgType)
	}
}

// In a private chat the chat grant can fail; the session's user is granted
// instead and the link still goes out.
func TestSendFile_DriveShareFallsBackToSessionUser(t *testing.T) {
	rec := &driveRecorder{blockSize: 10, rejectMembers: map[string]bool{"openchat": true}}
	p := newDriveTestPlatform(t, rec)
	p.imFileMaxBytes = 5
	rc := replyContext{chatID: testDriveChat, sessionKey: "feishu:" + testDriveChat + ":" + testDriveOwner}

	if err := p.SendFile(context.Background(), rc, core.FileAttachment{FileName: "a.bin", Data: []byte("0123456789")}); err != nil {
		t.Fatalf("SendFile() error = %v", err)
	}
	want := []driveMember{
		{docType: "file", memberType: "openchat", memberID: testDriveChat, perm: "view", kind: "chat"},
		{docType: "file", memberType: "openid", memberID: testDriveOwner, perm: "view", kind: "user"},
	}
	if len(rec.members) != 2 || rec.members[0] != want[0] || rec.members[1] != want[1] {
		t.Fatalf("permission members = %+v, want %+v", rec.members, want)
	}
	if rec.msgType != larkim.MsgTypePost || len(rec.deleted) != 0 {
		t.Fatalf("msg_type = %q deleted = %v, want post and nothing deleted", rec.msgType, rec.deleted)
	}
}

// When nobody can be given access, the upload is deleted and no dead link
// is sent.
func TestSendFile_DriveShareFailureDeletesUpload(t *testing.T) {
	rec := &driveRecorder{blockSize: 10, rejectMembers: map[string]bool{"openchat": true, "openid": true}}
	p := newDriveTestPlatform(t, rec)
	p.imFileMaxBytes = 5
	rc := replyContext{chatID: testDriveChat, sessionKey: "feishu:" + testDriveChat + ":" + testDriveOwner}

	err := p.SendFile(context.Background(), rc, core.FileAttachment{FileName: "a.bin", Data: []byte("0123456789")})
	if err == nil || !strings.Contains(err.Error(), "share cloud-space file") || !strings.Contains(err.Error(), "1063001") {
		t.Fatalf("SendFile() error = %v, want a share error carrying Feishu's code", err)
	}
	if len(rec.deleted) != 1 || rec.deleted[0] != testDriveFile+"?type=file" {
		t.Fatalf("deleted = %v, want the uploaded file", rec.deleted)
	}
	if rec.msgType != "" {
		t.Fatalf("a message was sent (msg_type %q), want none", rec.msgType)
	}
}

// drive_fallback = false fails before uploading anything and names the
// option.
func TestSendFile_DriveFallbackOffFailsEarly(t *testing.T) {
	rec := &driveRecorder{blockSize: 10}
	p := newDriveTestPlatform(t, rec)
	p.imFileMaxBytes = 5
	p.noDriveFallback = true

	err := p.SendFile(context.Background(), replyContext{chatID: testDriveChat}, core.FileAttachment{
		FileName: "big.zip", Data: []byte("0123456789"),
	})
	if err == nil || !strings.Contains(err.Error(), "drive_fallback") {
		t.Fatalf("SendFile() error = %v, want one naming drive_fallback", err)
	}
	if rec.rootCalls != 0 || rec.prepare != nil || len(rec.imUploads) != 0 || rec.msgType != "" {
		t.Fatalf("calls made: root=%d prepare=%v im=%v msg=%q; want none", rec.rootCalls, rec.prepare, rec.imUploads, rec.msgType)
	}
}

// drive_folder_token replaces the root folder lookup.
func TestSendFile_DriveFolderTokenUsed(t *testing.T) {
	rec := &driveRecorder{blockSize: 10}
	p := newDriveTestPlatform(t, rec)
	p.imFileMaxBytes = 5
	p.driveFolderToken = "fldConfigured"

	if err := p.SendFile(context.Background(), replyContext{chatID: testDriveChat}, core.FileAttachment{
		FileName: "a.bin", Data: []byte("0123456789"),
	}); err != nil {
		t.Fatalf("SendFile() error = %v", err)
	}
	if rec.rootCalls != 0 || rec.prepare["parent_node"] != "fldConfigured" {
		t.Fatalf("root lookups = %d, parent_node = %v; want 0 and fldConfigured", rec.rootCalls, rec.prepare["parent_node"])
	}
}

// A video over the limit, sent with SendVideo, goes the same way.
func TestSendVideo_OverLimitSendsCloudSpaceLink(t *testing.T) {
	rec := &driveRecorder{blockSize: 10}
	p := newDriveTestPlatform(t, rec)
	p.imFileMaxBytes = 5

	if err := p.SendVideo(context.Background(), replyContext{chatID: testDriveChat}, []byte("0123456789"), "mp4", "demo.mp4"); err != nil {
		t.Fatalf("SendVideo() error = %v", err)
	}
	if len(rec.imUploads) != 0 || rec.prepare["file_name"] != "demo.mp4" || rec.msgType != larkim.MsgTypePost {
		t.Fatalf("im uploads = %v, prepare = %v, msg_type = %q; want a drive upload of demo.mp4 sent as post",
			rec.imUploads, rec.prepare, rec.msgType)
	}
}

// An image over the image-message limit is sent as a file, named after its
// type when it has no name.
func TestSendImage_OverImageLimitSentAsFile(t *testing.T) {
	rec := &driveRecorder{blockSize: 10}
	p := newDriveTestPlatform(t, rec)
	p.imImageMaxBytes = 5
	p.imFileMaxBytes = 100

	err := p.SendImage(context.Background(), replyContext{chatID: testDriveChat}, core.ImageAttachment{
		MimeType: "image/png", Data: []byte("0123456789"),
	})
	if err != nil {
		t.Fatalf("SendImage() error = %v", err)
	}
	if rec.imageUpload != 0 || len(rec.imUploads) != 1 || rec.imUploads[0] != "image.png" {
		t.Fatalf("image uploads = %d, file uploads = %v; want 0 and [image.png]", rec.imageUpload, rec.imUploads)
	}
	if rec.msgType != larkim.MsgTypeFile {
		t.Fatalf("msg_type = %q, want file", rec.msgType)
	}
}

func TestDriveGrantees(t *testing.T) {
	p := &Platform{platformName: "feishu", dmChats: openDMChats("")}
	p.dmChats.remember("ou_dm_owner", "oc_dm")

	cases := []struct {
		name string
		rc   replyContext
		want []driveGrantee
	}{
		{"topic session grants the chat", replyContext{chatID: "oc_g", sessionKey: "feishu:oc_g:root:om_1"},
			[]driveGrantee{{"openchat", "oc_g", "chat"}}},
		{"user session grants chat then user", replyContext{chatID: "oc_p", sessionKey: "feishu:oc_p:ou_u"},
			[]driveGrantee{{"openchat", "oc_p", "chat"}, {"openid", "ou_u", "user"}}},
		{"shared private chat finds its user", replyContext{chatID: "oc_dm", sessionKey: "feishu:oc_dm"},
			[]driveGrantee{{"openchat", "oc_dm", "chat"}, {"openid", "ou_dm_owner", "user"}}},
		{"shared group session grants the chat", replyContext{chatID: "oc_g", sessionKey: "feishu:oc_g"},
			[]driveGrantee{{"openchat", "oc_g", "chat"}}},
		{"open_id chat grants the user", replyContext{chatID: "ou_menu", sessionKey: "feishu:ou_menu:ou_menu"},
			[]driveGrantee{{"openid", "ou_menu", "user"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := p.driveGrantees(c.rc)
			if len(got) != len(c.want) {
				t.Fatalf("driveGrantees() = %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("driveGrantees() = %+v, want %+v", got, c.want)
				}
			}
		})
	}
}

func TestNewPlatform_DriveOptions(t *testing.T) {
	base := map[string]any{"app_id": "cli_x", "app_secret": "s", "enable_feishu_card": false}
	with := func(extra map[string]any) map[string]any {
		opts := map[string]any{}
		for k, v := range base {
			opts[k] = v
		}
		for k, v := range extra {
			opts[k] = v
		}
		return opts
	}

	plat, err := New(base)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if p := plat.(*Platform); p.noDriveFallback || p.driveFolderToken != "" {
		t.Fatalf("defaults: noDriveFallback=%v driveFolderToken=%q, want false and empty", p.noDriveFallback, p.driveFolderToken)
	}

	plat, err = New(with(map[string]any{"drive_fallback": false, "drive_folder_token": " fldX "}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if p := plat.(*Platform); !p.noDriveFallback || p.driveFolderToken != "fldX" {
		t.Fatalf("noDriveFallback=%v driveFolderToken=%q, want true and fldX", p.noDriveFallback, p.driveFolderToken)
	}

	if _, err := New(with(map[string]any{"drive_fallback": "no"})); err == nil || !strings.Contains(err.Error(), "drive_fallback") {
		t.Fatalf("New() with drive_fallback=\"no\" error = %v, want an invalid drive_fallback error", err)
	}
}

func TestFormatFileSize(t *testing.T) {
	cases := map[int64]string{
		0:             "0 B",
		999:           "999 B",
		1000:          "1.0 KB",
		29_000_000:    "29.0 MB",
		35_240_000:    "35.2 MB",
		2_500_000_000: "2.5 GB",
	}
	for n, want := range cases {
		if got := formatFileSize(n); got != want {
			t.Errorf("formatFileSize(%d) = %q, want %q", n, got, want)
		}
	}
}
