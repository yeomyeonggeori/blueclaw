package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const connectorWorkspaceRootPath = "/workspace"

const mostAttachmentBytesCarried = 16 << 20

// The bridges that fetch an attachment run beside the workspace, not inside it:
// what they write lands on their own filesystem and the agent's /workspace never
// sees it. They hand over the bytes, and the file is written here, as the person
// the message came from, into their own inbox.
type importedAttachmentWriter struct {
	workspaceActorFactory security.WorkspaceActorFactory
	personID              string
}

func (writer importedAttachmentWriter) writeAll(ctx context.Context, attachments []InputAttachment) ([]InputAttachment, writtenAttachmentContents) {
	written := make([]InputAttachment, 0, len(attachments))
	contents := writtenAttachmentContents{}
	takenPaths := map[string]bool{}
	for _, attachment := range attachments {
		writtenAttachment, content := writer.write(ctx, attachment, takenPaths)
		written = append(written, writtenAttachment)
		if writtenAttachment.IsAvailable && len(content) > 0 {
			contents[writtenAttachment.Path] = content
		}
	}
	return written, contents
}

// The bytes that were just written, kept for the one hop that puts a picture in
// front of the model with the message it came on, instead of costing a tool
// call to look at what was just sent.
type writtenAttachmentContents map[string][]byte

func (writer importedAttachmentWriter) write(ctx context.Context, attachment InputAttachment, takenPaths map[string]bool) (InputAttachment, []byte) {
	content := strings.TrimSpace(attachment.ContentBase64)
	attachment.ContentBase64 = ""
	if content == "" {
		return attachment, nil
	}
	decoded, errorValue := base64.StdEncoding.DecodeString(content)
	if errorValue != nil {
		return refusedInputAttachment(attachment, errorValue), nil
	}
	actor, errorValue := writer.requesterActor(ctx)
	if errorValue != nil {
		return refusedInputAttachment(attachment, errorValue), nil
	}
	directoryPath := path.Dir(attachment.Path)
	if errorValue := actor.MkdirAll(ctx, directoryPath); errorValue != nil {
		return refusedInputAttachment(attachment, errorValue), nil
	}
	filePath := freeAttachmentPath(ctx, actor, attachment.Path, decoded, takenPaths)
	takenPaths[filePath] = true
	if errorValue := actor.WriteFile(ctx, filePath, decoded); errorValue != nil {
		return refusedInputAttachment(attachment, errorValue), nil
	}
	attachment.Path = filePath
	attachment.Filename = path.Base(filePath)
	attachment.SizeBytes = int64(len(decoded))
	attachment.IsAvailable = true
	attachment.ErrorCode = ""
	attachment.Message = ""
	return attachment, decoded
}

func (writer importedAttachmentWriter) requesterActor(ctx context.Context) (security.WorkspaceActor, error) {
	if writer.workspaceActorFactory == nil {
		return nil, errors.New("no workspace identity to write an attachment as")
	}
	if strings.TrimSpace(writer.personID) == "" {
		return nil, errors.New("an attachment is written as the person it was sent to, and this message names nobody")
	}
	return writer.workspaceActorFactory.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      policy.PersonAccess{PersonID: writer.personID},
		WorkspaceRootPath: connectorWorkspaceRootPath,
	})
}

// Two people can send the same name in one conversation, and the same picture
// can arrive twice. Identical content keeps the name it already has; different
// content under a taken name gets a number.
func freeAttachmentPath(ctx context.Context, actor security.WorkspaceActor, filePath string, content []byte, takenPaths map[string]bool) string {
	candidate := filePath
	for suffix := 2; suffix < 100; suffix++ {
		if !takenPaths[candidate] && !holdsOtherContent(ctx, actor, candidate, content) {
			return candidate
		}
		candidate = numberedAttachmentPath(filePath, suffix)
	}
	return candidate
}

func holdsOtherContent(ctx context.Context, actor security.WorkspaceActor, filePath string, content []byte) bool {
	existing, errorValue := actor.ReadFile(ctx, filePath, mostAttachmentBytesCarried)
	if errorValue != nil {
		return false
	}
	return sha256.Sum256(existing) != sha256.Sum256(content)
}

func numberedAttachmentPath(filePath string, suffix int) string {
	extension := path.Ext(filePath)
	return strings.TrimSuffix(filePath, extension) + "-" + strconv.Itoa(suffix) + extension
}

func refusedInputAttachment(attachment InputAttachment, errorValue error) InputAttachment {
	attachment.Path = ""
	attachment.ContentBase64 = ""
	attachment.IsAvailable = false
	attachment.ErrorCode = connectorAttachmentImportRefusedCode
	attachment.Message = errorValue.Error()
	return attachment
}
