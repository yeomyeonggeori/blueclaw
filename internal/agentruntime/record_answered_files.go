package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const answeredFilesDirectoryName = "answered"

type answeredFileKeeper struct {
	workspaceActorFactory security.WorkspaceActorFactory
	workspaceRootPath     string
	personAccess          policy.PersonAccess
}

type embeddedResource struct {
	Type     string `json:"type"`
	Resource struct {
		URI      string  `json:"uri"`
		MimeType string  `json:"mimeType"`
		Text     *string `json:"text"`
		Blob     *string `json:"blob"`
	} `json:"resource"`
}

type answeredFileLink struct {
	Type     string `json:"type"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType,omitempty"`
}

type answeredFileNotice struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (keeper answeredFileKeeper) withFilesKept(ctx context.Context, toolName string, input json.RawMessage, result mcp.ToolResult) mcp.ToolResult {
	if !carriesAnEmbeddedResource(result.Content) {
		return result
	}
	directoryPath, actor, destinationError := keeper.destination(ctx, toolName, input)
	content := make([]json.RawMessage, 0, len(result.Content))
	for _, item := range result.Content {
		resource, isResource := embeddedResourceIn(item)
		if !isResource {
			content = append(content, item)
			continue
		}
		content = append(content, keptFileContent(ctx, actor, directoryPath, resource, destinationError))
	}
	result.Content = content
	return result
}

func carriesAnEmbeddedResource(content []json.RawMessage) bool {
	for _, item := range content {
		if _, isResource := embeddedResourceIn(item); isResource {
			return true
		}
	}
	return false
}

func embeddedResourceIn(item json.RawMessage) (embeddedResource, bool) {
	var resource embeddedResource
	if json.Unmarshal(item, &resource) != nil || resource.Type != "resource" {
		return embeddedResource{}, false
	}
	return resource, true
}

func (keeper answeredFileKeeper) destination(ctx context.Context, toolName string, input json.RawMessage) (string, security.WorkspaceActor, error) {
	if keeper.workspaceActorFactory == nil {
		return "", nil, errors.New("no workspace identity to write it as")
	}
	taskRunID := strings.TrimSpace(toolcontract.TaskRunIDFromContext(ctx))
	personID := strings.TrimSpace(keeper.personAccess.PersonID)
	if taskRunID == "" || personID == "" {
		return "", nil, errors.New("this call belongs to no task of a person to keep it in")
	}
	taskDirectoryPath := security.TaskTemporaryDirectoryPath(security.PersonHomeDirectoryPath(keeper.workspaceRootPath, personID), taskRunID)
	if taskDirectoryPath == "" {
		return "", nil, errors.New("this task has no temporary directory")
	}
	actor, errorValue := keeper.workspaceActorFactory.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      keeper.personAccess,
		WorkspaceRootPath: keeper.workspaceRootPath,
	})
	if errorValue != nil {
		return "", nil, errorValue
	}
	directoryPath := filepath.Join(taskDirectoryPath, answeredFilesDirectoryName, answeredDirectoryName(toolName, input))
	if errorValue := actor.MkdirAll(ctx, directoryPath); errorValue != nil {
		return "", nil, errorValue
	}
	return directoryPath, actor, nil
}

func answeredDirectoryName(toolName string, input json.RawMessage) string {
	digest := sha256.Sum256(input)
	return filepath.Base(strings.TrimSpace(toolName)) + "-" + hex.EncodeToString(digest[:4])
}

func keptFileContent(ctx context.Context, actor security.WorkspaceActor, directoryPath string, resource embeddedResource, destinationError error) json.RawMessage {
	name := answeredFileName(resource.Resource.URI)
	if destinationError != nil {
		return noticeContent(name, destinationError)
	}
	if name == "" {
		return noticeContent(resource.Resource.URI, errors.New("its address names no file"))
	}
	content, errorValue := answeredFileBytes(resource)
	if errorValue != nil {
		return noticeContent(name, errorValue)
	}
	filePath := filepath.Join(directoryPath, name)
	if errorValue := actor.WriteFile(ctx, filePath, content); errorValue != nil {
		return noticeContent(name, errorValue)
	}
	encoded, _ := json.Marshal(answeredFileLink{
		Type:     "resource_link",
		URI:      (&url.URL{Scheme: "file", Path: filePath}).String(),
		Name:     name,
		MimeType: resource.Resource.MimeType,
	})
	return encoded
}

func answeredFileName(uri string) string {
	parsed, errorValue := url.Parse(strings.TrimSpace(uri))
	if errorValue != nil {
		return ""
	}
	name := path.Base(parsed.Path)
	if name == "." || name == ".." || name == "/" || strings.ContainsAny(name, `/\`) {
		return ""
	}
	return name
}

func answeredFileBytes(resource embeddedResource) ([]byte, error) {
	if resource.Resource.Blob != nil {
		return base64.StdEncoding.DecodeString(*resource.Resource.Blob)
	}
	if resource.Resource.Text != nil {
		return []byte(*resource.Resource.Text), nil
	}
	return nil, errors.New("it carried neither text nor bytes")
}

func noticeContent(name string, reason error) json.RawMessage {
	encoded, _ := json.Marshal(answeredFileNotice{
		Type: "text",
		Text: "The record answered " + name + " as a file, and it could not be kept: " + reason.Error(),
	})
	return encoded
}
