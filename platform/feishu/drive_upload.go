package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/adler32"
	"log/slog"
	"mime"
	"strconv"
	"strings"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Feishu's IM upload APIs cap what a bot can send as a file or image
// message: im/v1/files rejects files over 30 MB and im/v1/images images over
// 10 MB (error 234006). Larger files are uploaded to the app's cloud space
// (云空间) instead, shared with the chat, and sent as a link; see
// sendViaDrive. The limits are in decimal megabytes so they stay under
// Feishu's caps whether Feishu counts a megabyte as 10^6 or 2^20 bytes.
const (
	defaultIMFileMaxBytes  int64 = 29 * 1000 * 1000
	defaultIMImageMaxBytes int64 = 10 * 1000 * 1000
)

// Drive API values. explorer is the only parent_type Feishu accepts for
// files uploaded to the cloud space; file is the type of an uploaded file in
// the permission, meta and delete APIs.
const (
	driveParentTypeExplorer = "explorer"
	driveDocTypeFile        = "file"
	drivePermView           = "view"
)

// imFileLimit is the largest file sent as a file message; larger ones go
// through sendViaDrive.
func (p *Platform) imFileLimit() int64 {
	if p.imFileMaxBytes > 0 {
		return p.imFileMaxBytes
	}
	return defaultIMFileMaxBytes
}

// imImageLimit is the largest image sent as an image message; larger ones
// are sent as files.
func (p *Platform) imImageLimit() int64 {
	if p.imImageMaxBytes > 0 {
		return p.imImageMaxBytes
	}
	return defaultIMImageMaxBytes
}

// sendOversizedFile delivers a file too large for a file message: as a
// cloud-space link when drive_fallback is on (the default), otherwise it
// fails before uploading so the error names the limit instead of Feishu's
// code 234006.
func (p *Platform) sendOversizedFile(ctx context.Context, rc replyContext, data []byte, fileName string) error {
	if p.noDriveFallback {
		return fmt.Errorf("%s: %s is %s, over the %s limit for file messages; set drive_fallback = true to send it as a cloud-space link",
			p.tag(), fileName, formatFileSize(int64(len(data))), formatFileSize(p.imFileLimit()))
	}
	return p.sendViaDrive(ctx, rc, data, fileName)
}

// sendViaDrive uploads data to the app's cloud space, gives the chat read
// access, and sends a message linking to the file. If any step after the
// upload fails, the uploaded file is deleted so the space does not collect
// files nobody was told about.
func (p *Platform) sendViaDrive(ctx context.Context, rc replyContext, data []byte, fileName string) error {
	folder, err := p.driveFolder(ctx)
	if err != nil {
		return err
	}
	fileToken, err := p.uploadDriveFile(ctx, folder, fileName, data)
	if err != nil {
		return err
	}
	discard := func(cause error) error {
		p.deleteDriveFile(ctx, fileToken)
		return cause
	}
	if err := p.shareDriveFile(ctx, fileToken, rc); err != nil {
		return discard(err)
	}
	link, err := p.driveFileURL(ctx, fileToken)
	if err != nil {
		return discard(err)
	}
	content, err := buildDriveLinkPost(fileName, int64(len(data)), link)
	if err != nil {
		return discard(fmt.Errorf("%s: build cloud-space link message: %w", p.tag(), err))
	}
	if err := p.sendMediaMessage(ctx, rc, larkim.MsgTypePost, content); err != nil {
		return discard(err)
	}
	slog.Info(p.tag()+": file over the file-message limit sent as a cloud-space link",
		"file_name", fileName, "size", len(data), "file_token", fileToken, "chat_id", rc.chatID)
	return nil
}

// driveFolder returns the folder uploads go to: drive_folder_token when set,
// otherwise the root folder of the app's own cloud space, looked up once.
func (p *Platform) driveFolder(ctx context.Context) (string, error) {
	if p.driveFolderToken != "" {
		return p.driveFolderToken, nil
	}
	p.driveRootMu.Lock()
	defer p.driveRootMu.Unlock()
	if p.driveRootToken != "" {
		return p.driveRootToken, nil
	}
	const op = "get cloud-space root folder"
	var token string
	err := p.withTransientRetry(ctx, op, func() error {
		return p.withFreshTenantAccessTokenRetry(ctx, op, func(client *lark.Client, options ...larkcore.RequestOptionFunc) error {
			resp, err := client.Get(ctx, "/open-apis/drive/explorer/v2/root_folder/meta", nil, larkcore.AccessTokenTypeTenant, options...)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", p.tag(), op, err)
			}
			var body struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
				Data struct {
					Token string `json:"token"`
				} `json:"data"`
			}
			if err := json.Unmarshal(resp.RawBody, &body); err != nil {
				return fmt.Errorf("%s: %s: decode response (http %d): %w", p.tag(), op, resp.StatusCode, err)
			}
			if body.Code != 0 {
				return fmt.Errorf("%s: %s code=%d msg=%s", p.tag(), op, body.Code, body.Msg)
			}
			token = body.Data.Token
			return nil
		})
	})
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", fmt.Errorf("%s: %s: no folder token returned", p.tag(), op)
	}
	p.driveRootToken = token
	return token, nil
}

// uploadDriveFile uploads data to folder with Feishu's multipart upload
// (prepare, one call per block, finish) and returns the new file's token.
// Feishu picks the block size; every block but the last is exactly that
// size.
func (p *Platform) uploadDriveFile(ctx context.Context, folder, fileName string, data []byte) (string, error) {
	uploadID, blockSize, blockNum, err := p.prepareDriveUpload(ctx, folder, fileName, int64(len(data)))
	if err != nil {
		return "", err
	}
	if blockSize <= 0 {
		return "", fmt.Errorf("%s: prepare cloud-space upload: invalid block_size %d", p.tag(), blockSize)
	}
	if want := (len(data) + blockSize - 1) / blockSize; blockNum != want {
		return "", fmt.Errorf("%s: prepare cloud-space upload: block_num=%d, want %d for %d bytes in %d-byte blocks",
			p.tag(), blockNum, want, len(data), blockSize)
	}
	for seq := 0; seq < blockNum; seq++ {
		end := min((seq+1)*blockSize, len(data))
		if err := p.uploadDriveBlock(ctx, uploadID, seq, data[seq*blockSize:end]); err != nil {
			return "", err
		}
	}
	return p.finishDriveUpload(ctx, uploadID, blockNum)
}

func (p *Platform) prepareDriveUpload(ctx context.Context, folder, fileName string, size int64) (uploadID string, blockSize, blockNum int, err error) {
	const op = "prepare cloud-space upload"
	var resp *larkdrive.UploadPrepareFileResp
	err = p.withTransientRetry(ctx, op, func() error {
		return p.withFreshTenantAccessTokenRetry(ctx, op, func(client *lark.Client, options ...larkcore.RequestOptionFunc) error {
			req := larkdrive.NewUploadPrepareFileReqBuilder().
				FileUploadInfo(larkdrive.NewFileUploadInfoBuilder().
					FileName(fileName).
					ParentType(driveParentTypeExplorer).
					ParentNode(folder).
					Size(int(size)).
					Build()).
				Build()
			var err error
			resp, err = client.Drive.File.UploadPrepare(ctx, req, options...)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", p.tag(), op, err)
			}
			if !resp.Success() {
				return fmt.Errorf("%s: %s code=%d msg=%s", p.tag(), op, resp.Code, resp.Msg)
			}
			return nil
		})
	})
	if err != nil {
		return "", 0, 0, err
	}
	if resp.Data == nil || resp.Data.UploadId == nil || resp.Data.BlockSize == nil || resp.Data.BlockNum == nil {
		return "", 0, 0, fmt.Errorf("%s: %s: incomplete response", p.tag(), op)
	}
	return *resp.Data.UploadId, *resp.Data.BlockSize, *resp.Data.BlockNum, nil
}

func (p *Platform) uploadDriveBlock(ctx context.Context, uploadID string, seq int, block []byte) error {
	op := fmt.Sprintf("upload cloud-space block %d", seq)
	checksum := strconv.FormatUint(uint64(adler32.Checksum(block)), 10)
	return p.withTransientRetry(ctx, op, func() error {
		return p.withFreshTenantAccessTokenRetry(ctx, op, func(client *lark.Client, options ...larkcore.RequestOptionFunc) error {
			// The reader is built inside the retry so every attempt sends
			// the whole block.
			req := larkdrive.NewUploadPartFileReqBuilder().
				Body(larkdrive.NewUploadPartFileReqBodyBuilder().
					UploadId(uploadID).
					Seq(seq).
					Size(len(block)).
					Checksum(checksum).
					File(bytes.NewReader(block)).
					Build()).
				Build()
			resp, err := client.Drive.File.UploadPart(ctx, req, options...)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", p.tag(), op, err)
			}
			if !resp.Success() {
				return fmt.Errorf("%s: %s code=%d msg=%s", p.tag(), op, resp.Code, resp.Msg)
			}
			return nil
		})
	})
}

func (p *Platform) finishDriveUpload(ctx context.Context, uploadID string, blockNum int) (string, error) {
	const op = "finish cloud-space upload"
	var resp *larkdrive.UploadFinishFileResp
	err := p.withTransientRetry(ctx, op, func() error {
		return p.withFreshTenantAccessTokenRetry(ctx, op, func(client *lark.Client, options ...larkcore.RequestOptionFunc) error {
			req := larkdrive.NewUploadFinishFileReqBuilder().
				Body(larkdrive.NewUploadFinishFileReqBodyBuilder().
					UploadId(uploadID).
					BlockNum(blockNum).
					Build()).
				Build()
			var err error
			resp, err = client.Drive.File.UploadFinish(ctx, req, options...)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", p.tag(), op, err)
			}
			if !resp.Success() {
				return fmt.Errorf("%s: %s code=%d msg=%s", p.tag(), op, resp.Code, resp.Msg)
			}
			return nil
		})
	})
	if err != nil {
		return "", err
	}
	if resp.Data == nil || resp.Data.FileToken == nil || *resp.Data.FileToken == "" {
		return "", fmt.Errorf("%s: %s: no file_token returned", p.tag(), op)
	}
	return *resp.Data.FileToken, nil
}

// driveGrantee is a chat or user that can be given access to a file.
type driveGrantee struct {
	memberType string // openchat or openid
	memberID   string
	kind       string // chat or user
}

// driveGrantees lists who to share a file sent for rc with, best first: the
// chat, then the user the session belongs to. Feishu documents openchat only
// for group chats, so in a private chat the chat grant may fail and the user
// grant is what gives access. A reply context whose chat is an open_id (menu
// clicks before the bot knows the user's chat) names only the user.
func (p *Platform) driveGrantees(rc replyContext) []driveGrantee {
	if strings.HasPrefix(rc.chatID, "ou_") {
		return []driveGrantee{{memberType: "openid", memberID: rc.chatID, kind: "user"}}
	}
	var out []driveGrantee
	if rc.chatID != "" {
		out = append(out, driveGrantee{memberType: "openchat", memberID: rc.chatID, kind: "chat"})
	}
	if userID := p.sessionUser(rc); userID != "" {
		out = append(out, driveGrantee{memberType: "openid", memberID: userID, kind: "user"})
	}
	return out
}

// sessionUser returns the open_id of the user whose session rc belongs to:
// the user part of a {platform}:{chat}:{user} key, or for a chat's shared
// session the user whose private chat it is. "" when rc is a topic or group
// session.
func (p *Platform) sessionUser(rc replyContext) string {
	parts := strings.SplitN(rc.sessionKey, ":", 3)
	if len(parts) == 3 && strings.HasPrefix(parts[2], "ou_") {
		return parts[2]
	}
	if len(parts) == 2 {
		return p.dmChats.userOf(rc.chatID)
	}
	return ""
}

// shareDriveFile gives read access to the first grantee Feishu accepts.
func (p *Platform) shareDriveFile(ctx context.Context, fileToken string, rc replyContext) error {
	grantees := p.driveGrantees(rc)
	if len(grantees) == 0 {
		return fmt.Errorf("%s: share cloud-space file: reply context names no chat or user", p.tag())
	}
	var errs []error
	for _, g := range grantees {
		err := p.addDriveFileMember(ctx, fileToken, g)
		if err == nil {
			return nil
		}
		slog.Warn(p.tag()+": share cloud-space file failed", "grantee", g.kind, "member_id", g.memberID, "error", err)
		errs = append(errs, err)
	}
	return fmt.Errorf("%s: share cloud-space file: %w", p.tag(), errors.Join(errs...))
}

func (p *Platform) addDriveFileMember(ctx context.Context, fileToken string, g driveGrantee) error {
	op := "add cloud-space file " + g.kind + " member"
	return p.withTransientRetry(ctx, op, func() error {
		return p.withFreshTenantAccessTokenRetry(ctx, op, func(client *lark.Client, options ...larkcore.RequestOptionFunc) error {
			req := larkdrive.NewCreatePermissionMemberReqBuilder().
				Token(fileToken).
				Type(driveDocTypeFile).
				BaseMember(larkdrive.NewBaseMemberBuilder().
					MemberType(g.memberType).
					MemberId(g.memberID).
					Perm(drivePermView).
					Type(g.kind).
					Build()).
				Build()
			resp, err := client.Drive.PermissionMember.Create(ctx, req, options...)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", p.tag(), op, err)
			}
			if !resp.Success() {
				return fmt.Errorf("%s: %s code=%d msg=%s", p.tag(), op, resp.Code, resp.Msg)
			}
			return nil
		})
	})
}

// driveFileURL returns the link that opens a cloud-space file. Feishu hosts
// files under each tenant's own domain, so the link comes from the meta API
// rather than being built from the token.
func (p *Platform) driveFileURL(ctx context.Context, fileToken string) (string, error) {
	const op = "get cloud-space file link"
	var resp *larkdrive.BatchQueryMetaResp
	err := p.withTransientRetry(ctx, op, func() error {
		return p.withFreshTenantAccessTokenRetry(ctx, op, func(client *lark.Client, options ...larkcore.RequestOptionFunc) error {
			req := larkdrive.NewBatchQueryMetaReqBuilder().
				MetaRequest(larkdrive.NewMetaRequestBuilder().
					RequestDocs([]*larkdrive.RequestDoc{
						larkdrive.NewRequestDocBuilder().DocToken(fileToken).DocType(driveDocTypeFile).Build(),
					}).
					WithUrl(true).
					Build()).
				Build()
			var err error
			resp, err = client.Drive.Meta.BatchQuery(ctx, req, options...)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", p.tag(), op, err)
			}
			if !resp.Success() {
				return fmt.Errorf("%s: %s code=%d msg=%s", p.tag(), op, resp.Code, resp.Msg)
			}
			return nil
		})
	})
	if err != nil {
		return "", err
	}
	if resp.Data != nil {
		for _, m := range resp.Data.Metas {
			if m != nil && m.Url != nil && *m.Url != "" {
				return *m.Url, nil
			}
		}
		for _, f := range resp.Data.FailedList {
			if f != nil && f.Code != nil {
				return "", fmt.Errorf("%s: %s: failed code=%d", p.tag(), op, *f.Code)
			}
		}
	}
	return "", fmt.Errorf("%s: %s: no url returned", p.tag(), op)
}

// deleteDriveFile removes a file uploaded for a send that then failed. It
// only logs failures: the send has already failed with the error that
// matters.
func (p *Platform) deleteDriveFile(ctx context.Context, fileToken string) {
	const op = "delete cloud-space file"
	err := p.withTransientRetry(ctx, op, func() error {
		return p.withFreshTenantAccessTokenRetry(ctx, op, func(client *lark.Client, options ...larkcore.RequestOptionFunc) error {
			req := larkdrive.NewDeleteFileReqBuilder().FileToken(fileToken).Type(driveDocTypeFile).Build()
			resp, err := client.Drive.File.Delete(ctx, req, options...)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", p.tag(), op, err)
			}
			if !resp.Success() {
				return fmt.Errorf("%s: %s code=%d msg=%s", p.tag(), op, resp.Code, resp.Msg)
			}
			return nil
		})
	})
	if err != nil {
		slog.Warn(p.tag()+": failed send left a file in the cloud space", "file_token", fileToken, "error", err)
	}
}

// buildDriveLinkPost builds the post message that stands in for a file
// message: the file name linking to the file, then its size. It has no
// words to translate, so it reads the same in every language.
func buildDriveLinkPost(fileName string, size int64, link string) (string, error) {
	post := map[string]any{
		"zh_cn": map[string]any{
			"content": [][]map[string]any{{
				{"tag": "text", "text": "📎 "},
				{"tag": "a", "text": fileName, "href": link},
				{"tag": "text", "text": " (" + formatFileSize(size) + ")"},
			}},
		},
	}
	b, err := json.Marshal(post)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// formatFileSize renders n bytes in decimal units, the way Feishu states its
// limits ("30 MB").
func formatFileSize(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, exp := float64(n)/unit, 0
	for value >= unit && exp < 3 {
		value /= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", value, "KMGT"[exp])
}

// imageAsFile turns an image too large for an image message into a file
// attachment. Images sent without a name get one whose extension matches
// their type, so the file opens as an image.
func imageAsFile(img core.ImageAttachment) core.FileAttachment {
	name := img.FileName
	if name == "" {
		name = "image" + imageExtension(img.MimeType)
	}
	return core.FileAttachment{MimeType: img.MimeType, Data: img.Data, FileName: name}
}

func imageExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ""
}
