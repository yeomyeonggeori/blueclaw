package agentruntime

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	TaskContextEnvironmentName  = "SKILL_TASK_CONTEXT"
	taskContextFileName         = "task-context.json"
	taskContextReadLimit        = 8 << 20
	taskRecordResultLimit       = 64 << 10
	taskAttachmentTextRuneLimit = 200_000
)

type taskContext struct {
	Requester   taskRequester    `json:"requester"`
	Today       string           `json:"today"`
	Request     []string         `json:"request"`
	Attachments []taskAttachment `json:"attachments"`
	Records     []taskRecord     `json:"records"`
}

type taskRequester struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type taskAttachment struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Text      string `json:"text,omitempty"`
	IsCurrent bool   `json:"current,omitempty"`
}

type taskRecord struct {
	Tool   string          `json:"tool"`
	Input  json.RawMessage `json:"input"`
	Result json.RawMessage `json:"result,omitempty"`
	Files  []taskFile      `json:"files,omitempty"`
}

type taskFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func taskContextPath(taskTemporaryDirectoryPath string) string {
	if strings.TrimSpace(taskTemporaryDirectoryPath) == "" {
		return ""
	}
	return filepath.Join(taskTemporaryDirectoryPath, taskContextFileName)
}

func (toolCatalogBuilder *ToolCatalogBuilder) taskContextPathFor(ctx context.Context, request ToolCatalogRequest) string {
	taskRunID := toolcontract.TaskRunIDFromContext(ctx)
	return taskContextPath(security.TaskTemporaryDirectoryPath(toolCatalogBuilder.requesterHomePath(request), taskRunID))
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeTaskContext(ctx context.Context, actor security.WorkspaceActor, request ToolCatalogRequest, newRecords ...taskRecord) error {
	contextPath := toolCatalogBuilder.taskContextPathFor(ctx, request)
	if contextPath == "" {
		return nil
	}
	toolCatalogBuilder.taskContextMutex.Lock()
	defer toolCatalogBuilder.taskContextMutex.Unlock()
	document := toolCatalogBuilder.taskContextFor(request, toolcontract.TaskRunIDFromContext(ctx))
	document.Records = append(recordedTaskRecords(ctx, actor, contextPath), newRecords...)
	if errorValue := actor.MkdirAll(ctx, filepath.Dir(contextPath)); errorValue != nil {
		return errorValue
	}
	return actor.WriteFile(ctx, contextPath, []byte(MarshalBody(document)))
}

func recordedTaskRecords(ctx context.Context, actor security.WorkspaceActor, contextPath string) []taskRecord {
	content, errorValue := actor.ReadFile(ctx, contextPath, taskContextReadLimit)
	if errorValue != nil {
		return []taskRecord{}
	}
	var recorded taskContext
	if json.Unmarshal(content, &recorded) != nil || recorded.Records == nil {
		return []taskRecord{}
	}
	return recorded.Records
}

func (toolCatalogBuilder *ToolCatalogBuilder) taskContextFor(request ToolCatalogRequest, taskRunID string) taskContext {
	return taskContext{
		Requester:   taskRequester{Name: strings.TrimSpace(request.RequesterName), Email: strings.TrimSpace(request.RequesterEmail)},
		Today:       toolCatalogBuilder.taskStartedAt(taskRunID).In(toolCatalogBuilder.companyLocation()).Format(time.DateOnly),
		Request:     requestWordings(request),
		Attachments: toolCatalogBuilder.taskAttachments(request),
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) taskAttachments(request ToolCatalogRequest) []taskAttachment {
	current := map[string]bool{}
	for _, material := range request.VisibleContext.CurrentMaterials {
		current[visibleAttachmentMaterialKey(material)] = true
	}
	attachments := []taskAttachment{}
	for _, material := range visibleAttachmentMaterials(request.VisibleContext) {
		attachments = append(attachments, taskAttachment{
			Name:      firstNonEmptyString(strings.TrimSpace(material.Filename), filepath.Base(strings.TrimSpace(material.Path))),
			Path:      toolCatalogBuilder.availablePath(request, material.Path, material.IsAvailable),
			Text:      boundedRunes(strings.TrimSpace(material.MarkdownPreview), taskAttachmentTextRuneLimit),
			IsCurrent: current[visibleAttachmentMaterialKey(material)],
		})
	}
	return attachments
}

func (toolCatalogBuilder *ToolCatalogBuilder) availablePath(request ToolCatalogRequest, path string, isAvailable bool) string {
	if !isAvailable || strings.TrimSpace(path) == "" {
		return ""
	}
	return toolCatalogBuilder.nativeRequesterPath(request, path)
}

func boundedRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func requestWordings(request ToolCatalogRequest) []string {
	wordings := []string{}
	for _, message := range request.VisibleContext.Messages {
		if text := strings.TrimSpace(message.Text); text != "" {
			wordings = append(wordings, text)
		}
	}
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		wordings = append(wordings, prompt)
	}
	return wordings
}

func (toolCatalogBuilder *ToolCatalogBuilder) recordAnsweredRecordTool(ctx context.Context, request ToolCatalogRequest, toolName string, input json.RawMessage, result mcp.ToolResult) {
	if result.IsError {
		return
	}
	actor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(ctx, request)
	if actorFailure != nil {
		return
	}
	_ = toolCatalogBuilder.writeTaskContext(ctx, actor, request, answeredTaskRecord(toolName, input, result))
}

func answeredTaskRecord(toolName string, input json.RawMessage, result mcp.ToolResult) taskRecord {
	record := taskRecord{Tool: toolName, Input: compactJSONOrEmptyObject(input), Files: keptFiles(result.Content)}
	if answered := resultInsideTheEnvelope(result.StructuredContent); len(answered) > 0 && len(answered) <= taskRecordResultLimit {
		record.Result = answered
	}
	return record
}

func compactJSONOrEmptyObject(input json.RawMessage) json.RawMessage {
	var value any
	if json.Unmarshal(input, &value) != nil {
		return json.RawMessage(`{}`)
	}
	compacted, _ := json.Marshal(value)
	return compacted
}

func keptFiles(content []json.RawMessage) []taskFile {
	files := []taskFile{}
	for _, item := range content {
		var link answeredFileLink
		if json.Unmarshal(item, &link) != nil || link.Type != "resource_link" {
			continue
		}
		parsed, errorValue := url.Parse(link.URI)
		if errorValue != nil || parsed.Scheme != "file" {
			continue
		}
		files = append(files, taskFile{Name: link.Name, Path: parsed.Path})
	}
	return files
}

func (toolCatalogBuilder *ToolCatalogBuilder) taskStartedAt(taskRunID string) time.Time {
	createdAt, isKnown := toolCatalogBuilder.taskRunCreatedAt(taskRunID)
	if !isKnown {
		return time.Now()
	}
	return createdAt
}

func (toolCatalogBuilder *ToolCatalogBuilder) taskRunCreatedAt(taskRunID string) (time.Time, bool) {
	if toolCatalogBuilder.taskRunService == nil || strings.TrimSpace(taskRunID) == "" {
		return time.Time{}, false
	}
	taskRun, isFound := toolCatalogBuilder.taskRunService.FindTaskRun(taskRunID)
	return taskRun.CreatedAt, isFound && !taskRun.CreatedAt.IsZero()
}

func (toolCatalogBuilder *ToolCatalogBuilder) companyLocation() *time.Location {
	location, errorValue := time.LoadLocation(strings.TrimSpace(toolCatalogBuilder.companyTimeZone()))
	if errorValue != nil {
		return time.UTC
	}
	return location
}
