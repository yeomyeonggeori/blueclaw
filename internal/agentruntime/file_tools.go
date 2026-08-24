package agentruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"mime"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const inlineAttachmentMaximumBytes = 25 * 1024 * 1024

const defaultFileReadMaximumBytes = 128 * 1024

const maximumFileReadBytes = 1024 * 1024

const maximumEditableTextFileBytes = 2 * 1024 * 1024

const maximumFilePreviewBytes = 200 * 1024

type fileReadToolInput struct {
	Path           string `json:"path"`
	FileHint       string `json:"fileHint"`
	MaterialID     string `json:"materialID"`
	MaxOutputBytes int    `json:"maxOutputBytes"`
	StartLine      int    `json:"startLine"`
	LineCount      int    `json:"lineCount"`
	StartByte      int    `json:"startByte"`
}

type filePreviewToolInput struct {
	Path       string `json:"path"`
	FileHint   string `json:"fileHint"`
	MaterialID string `json:"materialID"`
}

type fileWriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type fileDeleteToolInput struct {
	Path string `json:"path"`
}

type filePatchToolInput struct {
	Edits []filePatchEditInput `json:"edits"`
}

type filePatchEditInput struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type fileAttachToolInput struct {
	Path        string                `json:"path"`
	Filename    string                `json:"filename"`
	ContentType string                `json:"contentType"`
	Title       string                `json:"title"`
	Files       []fileAttachFileInput `json:"files"`
}

type fileAttachFileInput struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Title       string `json:"title"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerFileTools(toolRegistry *toolcontract.ToolSet, handlerContext toolHandlerContext) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[fileWriteToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "file_write",
			Description: "Overwrite one UTF-8 text file under the Blueclaw workspace. Treat content as the complete file body, like terminal redirection to a file: include the text exactly as it should appear in the file, with real line breaks for multiline source.",
			RecoveryCard: toolcontract.ToolRecoveryCard{
				Does:       "Overwrites one workspace text file with the exact content string.",
				Produces:   "A written source, document, script, or config file at the requested path.",
				SideEffect: "workspace_write",
				UseWhen:    "A new file must be created, or an existing file is being replaced wholesale.",
				AvoidWhen:  "An existing file only needs a targeted change — use file_edit to keep the rest of the work instead of rewriting the whole file; or you only need to inspect files, append shell output, or run commands. Do not pass escaped newline sequences when writing multiline source.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace path to create or overwrite."},"content":{"type":"string","description":"Complete file body as plain UTF-8 text. Use real line breaks for multiline files; this is the text that will be written exactly."}},"required":["path","content"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input fileWriteToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.writeFileTool(toolContext, input, handlerContext)
		},
		Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[fileReadToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "file_read",
			Description: "Read exact UTF-8 workspace text or a real file line range with honest size and truncation metadata. Use file_preview first for attached HTML, PDF, DOCX, PPTX, XLSX, or other documents.",
			RecoveryCard: toolcontract.ToolRecoveryCard{
				Does:       "Reads a text file or requested line range from the actual workspace file; attachment materialID falls back to cached preview text.",
				Produces:   "Text content plus path, line range, original size, returned size, line count if known, and truncation metadata.",
				SideEffect: "read",
				UseWhen:    "You need current file content before file_edit or file.write.",
				AvoidWhen:  "The file is binary, an attached document needing conversion, or you already have the exact current text needed for an edit.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace text file path to read."},"fileHint":{"type":"string","description":"Exact fileHint from Current attachments or Previous attachments."},"materialID":{"type":"string","description":"Attachment materialID from Current attachments or Previous attachments. Use file_preview first; file_read returns cached preview text if no exact workspace file is available."},"startLine":{"type":"integer","description":"Optional 1-based first line to return. Avoid for minified or few-line files; use startByte instead."},"lineCount":{"type":"integer","description":"Optional number of lines to return from startLine."},"startByte":{"type":"integer","description":"Optional 0-based byte offset for byte-range reads. Use this for minified or single-line files; continue from the nextByte value of the previous read until isEndOfFile is true."}},"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input fileReadToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.readFileTool(toolContext, input, handlerContext)
		},
		Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[filePreviewToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "file_preview",
			Description: "Preview an attached or workspace file path from the conversation attachment catalog using cached AgentPart markdownPreview when available, or the existing document_read anydoc provider for convertible documents.",
			RecoveryCard: toolcontract.ToolRecoveryCard{
				Does:       "Returns a document preview or file metadata without inventing content.",
				Produces:   "Path, filename, content type, size, markdown preview, conversion status, and conversion message.",
				SideEffect: "read",
				UseWhen:    "The attachment catalog lists a materialID or path for an HTML, PDF, DOCX, PPTX, XLSX, text, or data file and you need to understand it.",
				AvoidWhen:  "You need exact source lines for an edit; use file_read after previewing.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace file path to preview. Use this when the attachment catalog has a readable path."},"fileHint":{"type":"string","description":"Exact fileHint from Current attachments or Previous attachments."},"materialID":{"type":"string","description":"Attachment materialID from Current attachments or Previous attachments. Use this when the catalog lists a materialID, especially if no readable path is available."}},"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input filePreviewToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.previewFileTool(toolContext, input, handlerContext)
		},
		Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[filePatchToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "file_edit",
			Description: "Apply one or more exact text replacements to workspace files as one atomic edit. Each oldText must appear exactly once where it is applied. This is the tool for every targeted change to an existing file: pass a single edit for one change, or group related changes into one call. Read the file first so each oldText matches exactly.",
			RecoveryCard: toolcontract.ToolRecoveryCard{
				Does:       "Replaces exact oldText occurrences with newText across one or more workspace text files, all-or-nothing.",
				Produces:   "Modified source, document, script, or config files with match metadata.",
				SideEffect: "workspace_write",
				UseWhen:    "An existing file needs one or more targeted changes and the current oldText snippets are known.",
				AvoidWhen:  "The change creates a new file or replaces most of a file (use file_write), or oldText is missing or ambiguous (use file_read first).",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"edits":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string","description":"Workspace text file path to modify."},"oldText":{"type":"string","description":"Exact existing text to replace; must appear exactly once when this edit is applied."},"newText":{"type":"string","description":"Replacement text."}},"required":["path","oldText","newText"],"additionalProperties":false}}},"required":["edits"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input filePatchToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.patchFileTool(toolContext, input, handlerContext)
		},
		Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[fileDeleteToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:             "file_delete",
			RequiresApproval: true,
			Description:      "Delete one file from the Blueclaw workspace by its path. Use the same path form as file_write and file_read, for example ~/documents/notes.docx or ~/documents/report.pdf. The runtime pauses for the user's approval before the file is removed, so find the file yourself and call this directly — do not ask the user which file.",
			RecoveryCard: toolcontract.ToolRecoveryCard{
				Does:       "Removes one workspace file at the requested path.",
				Produces:   "Confirmation that the file no longer exists.",
				SideEffect: "workspace_write",
				UseWhen:    "A workspace file the requester created or owns should be removed; resolve the path with the same form used to write it.",
				AvoidWhen:  "You only need to overwrite a file (use file_write), the path is a directory, or it is a built-in resource.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace file path to delete, in the same form as file_write (for example tmp/notes.txt)."}},"required":["path"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input fileDeleteToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.deleteFileTool(toolContext, input, handlerContext)
		},
		Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[fileAttachToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:            toolcontract.FileDeliverToolName,
			Description:     "Deliver one or more existing workspace files as final reply evidence.",
			SideEffectClass: toolcontract.ToolSideEffectStateChange,
			InputSchema:     json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace path to one finished file."},"filename":{"type":"string","description":"Optional display filename."},"contentType":{"type":"string","description":"Optional MIME type."},"title":{"type":"string","description":"Optional attachment title."},"files":{"type":"array","description":"One or more finished workspace files to deliver in this single call.","items":{"type":"object","properties":{"path":{"type":"string","description":"Workspace path to an existing file."},"filename":{"type":"string","description":"Optional display filename."},"contentType":{"type":"string","description":"Optional MIME type."},"title":{"type":"string","description":"Optional attachment title."}},"required":["path"],"additionalProperties":false}}},"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input fileAttachToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.attachFileTool(toolContext, input, handlerContext)
		},
		Result: toolcontract.IdentityToolResult,
	})
}

func fileToolSuccess(document map[string]any) toolcontract.ToolResult {
	content := marshalToolResult(document)
	return toolcontract.ToolResult{
		Output: toolcontract.ToolOutput{Content: content, Data: json.RawMessage(content)},
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeFileTool(toolContext context.Context, input fileWriteToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_write", "path is required"), nil
	}
	if input.Content == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_write", "content is required"), nil
	}
	if failureResult := toolCatalogBuilder.runRequesterFileWrite(toolContext, handlerContext, "file_write", path, input.Content); failureResult != nil {
		return *failureResult, nil
	}
	return fileToolSuccess(map[string]any{
		"path":      path,
		"sizeBytes": len(input.Content),
	}), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) runRequesterFileWrite(toolContext context.Context, handlerContext toolHandlerContext, stage string, path string, content string) *toolcontract.ToolResult {
	outcome, actorFailure := toolCatalogBuilder.runRequesterShell(toolContext, handlerContext.request, requesterShellCommand{
		Command: fileWriteShellCommand(path),
		Stdin:   content,
	})
	if actorFailure != nil {
		return actorFailure
	}
	if outcome.RunError != nil {
		result := outcome.toolFailure("write_file", stage, path)
		return &result
	}
	return nil
}

func fileWriteShellCommand(path string) string {
	pathArgument := shellPathArgument(path)
	return `mkdir -p -- "$(dirname -- ` + pathArgument + `)" && cat > ` + pathArgument
}

func fileReadShellCommand(path string, readLimitBytes int) string {
	pathArgument := shellPathArgument(path)
	return "wc -c < " + pathArgument + " && head -c " + strconv.Itoa(readLimitBytes) + " < " + pathArgument
}

const fileReadShellOutputReserveBytes = 64

func parseFileReadShellOutput(stdout string) (int64, string, bool) {
	sizeLine, content, hasContent := strings.Cut(stdout, "\n")
	sizeBytes, errorValue := strconv.ParseInt(strings.TrimSpace(sizeLine), 10, 64)
	if errorValue != nil || sizeBytes < 0 {
		return 0, "", false
	}
	if !hasContent {
		return sizeBytes, "", true
	}
	return sizeBytes, content, true
}

func (toolCatalogBuilder *ToolCatalogBuilder) deleteFileTool(toolContext context.Context, input fileDeleteToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_delete", "path is required"), nil
	}
	outcome, actorFailure := toolCatalogBuilder.runRequesterShell(toolContext, handlerContext.request, requesterShellCommand{
		Command: "rm -- " + shellPathArgument(path),
	})
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if outcome.RunError != nil {
		return outcome.toolFailure("remove_file", "file_delete", path), nil
	}
	return fileToolSuccess(map[string]any{
		"path":    path,
		"deleted": true,
	}), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) readFileTool(toolContext context.Context, input fileReadToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	path, materialID, errorValue := resolveFileHintReference(handlerContext.request, input.Path, input.MaterialID, input.FileHint)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_read", errorValue.Error()), nil
	}
	input.Path = path
	input.MaterialID = materialID
	if materialID != "" {
		if result, isCached := cachedFileReadResultByMaterialID(handlerContext.request.InputParts, materialID, input); isCached {
			return result, nil
		}
	}
	if path == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_read", "path or materialID is required"), nil
	}
	maxOutputBytes := input.MaxOutputBytes
	if maxOutputBytes <= 0 || maxOutputBytes > maximumFileReadBytes {
		maxOutputBytes = defaultFileReadMaximumBytes
	}
	readMaximumBytes := maximumFileReadBytes
	if maxOutputBytes > readMaximumBytes {
		readMaximumBytes = maxOutputBytes
	}
	outcome, actorFailure := toolCatalogBuilder.runRequesterShell(toolContext, handlerContext.request, requesterShellCommand{
		Command:            fileReadShellCommand(path, readMaximumBytes+1),
		OutputMaximumBytes: readMaximumBytes + fileReadShellOutputReserveBytes,
	})
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if outcome.RunError != nil {
		if result, isCached := cachedFileReadResult(handlerContext.request.InputParts, path, input); isCached {
			return result, nil
		}
		if result, fallbackError, isFound := toolCatalogBuilder.fileReadFallbackFromAttachmentMaterial(toolContext, path, input, handlerContext); isFound {
			return result, fallbackError
		}
		if outcome.failureCode() == security.ActorErrorCodeNotFound && toolCatalogBuilder.isOptionalControlFilePath(path) {
			return toolCatalogBuilder.optionalControlFileMissingResult(path, input, maxOutputBytes), nil
		}
		return outcome.toolFailure("read_file", "file_read", path), nil
	}
	originalSizeBytes, content, isParsed := parseFileReadShellOutput(outcome.CommandResult.Stdout)
	if !isParsed {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "file_read", "file size probe returned unreadable output"), nil
	}
	if originalSizeBytes > int64(readMaximumBytes) {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_read", "file is too large for exact text read; use file_preview for document or attachment understanding"), nil
	}
	isFileTruncated := len(content) > readMaximumBytes
	if isFileTruncated {
		content = content[:readMaximumBytes]
	}
	if !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_read", "file_read supports UTF-8 text files; use file_preview or a specialized document tool for binary files"), nil
	}
	readResult := fileReadResult(content, input, maxOutputBytes)
	return fileToolSuccess(fileReadResultMap(map[string]any{
		"path":              path,
		"totalLinesKnown":   !isFileTruncated,
		"originalSizeBytes": originalSizeBytes,
		"sizeBytes":         originalSizeBytes,
		"isTruncated":       isFileTruncated || readResult.IsTruncated,
	}, readResult)), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) optionalControlFileMissingResult(path string, input fileReadToolInput, maxOutputBytes int) toolcontract.ToolResult {
	readResult := fileReadResult("", input, maxOutputBytes)
	readResult.ReadHint = "This control file is optional and is not present yet. Create or update it before source edits if it is relevant to the current workflow."
	result := fileReadResultMap(map[string]any{
		"path":              path,
		"exists":            false,
		"optional":          true,
		"totalLinesKnown":   true,
		"originalSizeBytes": 0,
		"sizeBytes":         0,
		"isTruncated":       false,
	}, readResult)
	if recommendedPath := toolCatalogBuilder.recommendedSiteControlWritePath(path); recommendedPath != "" {
		result["recommendedWritePath"] = recommendedPath
	}
	return fileToolSuccess(result)
}

func (toolCatalogBuilder *ToolCatalogBuilder) isOptionalControlFilePath(path string) bool {
	cleanPath := strings.Trim(filepath.ToSlash(strings.TrimSpace(path)), "/")
	for _, suffix := range toolCatalogBuilder.optionalFileReadPathSuffixes {
		if strings.HasSuffix(cleanPath, suffix) {
			return true
		}
	}
	return false
}

func (toolCatalogBuilder *ToolCatalogBuilder) recommendedSiteControlWritePath(path string) string {
	cleanPath := strings.Trim(filepath.ToSlash(strings.TrimSpace(path)), "/")
	for _, prefix := range []string{"~/sites/", "home/sites/", "workspace/circles/staff/sites/"} {
		if recommendedPath := toolCatalogBuilder.recommendedSiteControlWritePathForPrefix(cleanPath, prefix); recommendedPath != "" {
			return recommendedPath
		}
	}
	return ""
}

func (toolCatalogBuilder *ToolCatalogBuilder) recommendedSiteControlWritePathForPrefix(path string, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(path, prefix)
	siteID, relativePath, hasRelativePath := strings.Cut(remainder, "/")
	if strings.TrimSpace(siteID) == "" || !hasRelativePath {
		return ""
	}
	if strings.HasPrefix(relativePath, "draft/") {
		return ""
	}
	if !toolCatalogBuilder.isOptionalControlFilePath(relativePath) {
		return ""
	}
	return filepath.ToSlash(filepath.Join("/workspace", "circles", "staff", "sites", siteID, "draft", relativePath))
}

func fileReadResultMap(base map[string]any, readResult fileReadOutput) map[string]any {
	base["content"] = readResult.Content
	base["startLine"] = readResult.StartLine
	base["endLine"] = readResult.EndLine
	base["totalLines"] = readResult.TotalLines
	base["returnedBytes"] = len([]byte(readResult.Content))
	base["startByte"] = readResult.StartByte
	base["endByte"] = readResult.EndByte
	base["nextByte"] = readResult.NextByte
	base["totalBytes"] = readResult.TotalBytes
	base["isEndOfFile"] = readResult.IsEndOfFile
	if _, hasTruncated := base["isTruncated"]; !hasTruncated {
		base["isTruncated"] = readResult.IsTruncated
	}
	if strings.TrimSpace(readResult.ReadHint) != "" {
		base["readHint"] = readResult.ReadHint
	}
	return base
}

func cachedFileReadResultByMaterialID(parts []agentcontract.AgentPart, materialID string, input fileReadToolInput) (toolcontract.ToolResult, bool) {
	preview, isCached := cachedFilePreviewResultByMaterialID(parts, materialID)
	if !isCached {
		return toolcontract.ToolResult{}, false
	}
	return cachedFileReadResultFromPreview(preview, input), true
}

func cachedFileReadResult(parts []agentcontract.AgentPart, path string, input fileReadToolInput) (toolcontract.ToolResult, bool) {
	preview, isCached := cachedFilePreviewResult(parts, path)
	if !isCached {
		return toolcontract.ToolResult{}, false
	}
	return cachedFileReadResultFromPreview(preview, input), true
}

func cachedFileReadResultFromPreview(preview map[string]any, input fileReadToolInput) toolcontract.ToolResult {
	content := stringMapValue(preview, "markdownPreview")
	readResult := fileReadResult(content, input, defaultFileReadMaximumBytes)
	return fileToolSuccess(fileReadResultMap(map[string]any{
		"path":              stringMapValue(preview, "path"),
		"totalLinesKnown":   true,
		"originalSizeBytes": int64MapValue(preview, "sizeBytes"),
		"sizeBytes":         int64MapValue(preview, "sizeBytes"),
		"isTruncated":       readResult.IsTruncated,
		"source":            "attachmentPreview",
		"isExactFileRead":   false,
	}, readResult))
}

func (toolCatalogBuilder *ToolCatalogBuilder) fileReadFallbackFromAttachmentMaterial(toolContext context.Context, path string, input fileReadToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error, bool) {
	material, isFound := visibleAttachmentMaterialForPath(handlerContext.request.VisibleContext, path)
	if !isFound {
		return toolcontract.ToolResult{}, nil, false
	}
	materialID := strings.TrimSpace(material.MaterialID)
	if materialID == "" {
		return toolcontract.ToolResult{}, nil, false
	}
	resolvedMaterial, errorValue := resolveReadableAttachmentMaterial(toolContext, handlerContext.request, materialID)
	if errorValue != nil {
		return attachmentResolutionFailure("file_read", errorValue), nil, true
	}
	if attachmentMaterialLooksLikeImage(resolvedMaterial) {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_read", "attachment material is an image; use image_read"), nil, true
	}
	if preview, hasPreview := filePreviewResultFromVisibleMaterial(resolvedMaterial); hasPreview {
		return cachedFileReadResultFromPreview(preview, input), nil, true
	}
	fallbackPath := toolCatalogBuilder.resolveAgentWorkspacePath(resolvedMaterial.Path)
	if fallbackPath == "" || fallbackPath == strings.TrimSpace(path) {
		return toolcontract.ToolResult{}, nil, false
	}
	fallbackInput := input
	fallbackInput.Path = fallbackPath
	fallbackInput.MaterialID = ""
	result, readError := toolCatalogBuilder.readFileTool(toolContext, fallbackInput, handlerContext)
	return result, readError, true
}

func stringMapValue(document map[string]any, key string) string {
	value, _ := document[key].(string)
	return strings.TrimSpace(value)
}

func int64MapValue(document map[string]any, key string) int64 {
	switch value := document[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

type fileReadOutput struct {
	Content     string
	StartLine   int
	EndLine     int
	TotalLines  int
	IsTruncated bool
	StartByte   int
	EndByte     int
	NextByte    int
	TotalBytes  int
	IsEndOfFile bool
	ReadHint    string
}

func fileReadResult(content string, input fileReadToolInput, maxOutputBytes int) fileReadOutput {
	totalBytes := len(content)
	if input.StartByte > 0 {
		return byteWindowReadResult(content, input.StartByte, maxOutputBytes, totalBytes)
	}
	lines := splitFileLines(content)
	totalLines := len(lines)
	if totalLines == 0 {
		return fileReadOutput{TotalBytes: totalBytes, IsEndOfFile: true}
	}
	if input.StartLine <= 0 {
		windowedContent, isTruncated := truncateTextByBytes(content, maxOutputBytes)
		nextByte := 0
		if isTruncated {
			nextByte = len(windowedContent)
		}
		return fileReadOutput{
			Content:     windowedContent,
			StartLine:   1,
			EndLine:     totalLines,
			TotalLines:  totalLines,
			IsTruncated: isTruncated,
			EndByte:     len(windowedContent),
			NextByte:    nextByte,
			TotalBytes:  totalBytes,
			IsEndOfFile: !isTruncated,
		}
	}
	if input.StartLine > totalLines {
		return fileReadOutput{
			StartLine:   input.StartLine,
			EndLine:     input.StartLine - 1,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			IsEndOfFile: true,
			ReadHint:    "startLine " + strconv.Itoa(input.StartLine) + " is past totalLines " + strconv.Itoa(totalLines) + "; this file has few lines but " + strconv.Itoa(totalBytes) + " bytes. Re-read with startByte for byte-range reads instead of paginating by line.",
		}
	}
	lineCount := input.LineCount
	if lineCount <= 0 {
		lineCount = 200
	}
	endLine := input.StartLine + lineCount - 1
	if endLine > totalLines {
		endLine = totalLines
	}
	windowedContent, isTruncated := truncateTextByBytes(strings.Join(lines[input.StartLine-1:endLine], "\n"), maxOutputBytes)
	return fileReadOutput{
		Content:     windowedContent,
		StartLine:   input.StartLine,
		EndLine:     endLine,
		TotalLines:  totalLines,
		IsTruncated: isTruncated,
		TotalBytes:  totalBytes,
		IsEndOfFile: endLine >= totalLines && !isTruncated,
	}
}

func byteWindowReadResult(content string, startByte int, maxOutputBytes int, totalBytes int) fileReadOutput {
	start := startByte
	if start > totalBytes {
		start = totalBytes
	}
	start = snapForwardToRuneBoundary(content, start)
	end := start + maxOutputBytes
	if end > totalBytes {
		end = totalBytes
	}
	end = snapBackToRuneBoundary(content, end, start)
	windowedContent := content[start:end]
	nextByte := 0
	if end < totalBytes {
		nextByte = end
	}
	return fileReadOutput{
		Content:     windowedContent,
		StartByte:   start,
		EndByte:     end,
		NextByte:    nextByte,
		TotalBytes:  totalBytes,
		IsTruncated: nextByte > 0,
		IsEndOfFile: nextByte == 0,
	}
}

func snapForwardToRuneBoundary(content string, index int) int {
	for index < len(content) && !utf8.RuneStart(content[index]) {
		index++
	}
	return index
}

func snapBackToRuneBoundary(content string, index int, minimum int) int {
	for index > minimum && index < len(content) && !utf8.RuneStart(content[index]) {
		index--
	}
	return index
}

func splitFileLines(content string) []string {
	if content == "" {
		return nil
	}
	normalizedContent := strings.TrimSuffix(content, "\n")
	if normalizedContent == "" {
		return []string{""}
	}
	return strings.Split(normalizedContent, "\n")
}

func (toolCatalogBuilder *ToolCatalogBuilder) previewFileTool(toolContext context.Context, input filePreviewToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	path, materialID, errorValue := resolveFileHintReference(handlerContext.request, input.Path, input.MaterialID, input.FileHint)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_preview", errorValue.Error()), nil
	}
	input.Path = path
	input.MaterialID = materialID
	if cachedPreview, isCached := cachedFilePreviewResultForInput(handlerContext.request.InputParts, input); isCached {
		return fileToolSuccess(cachedPreview), nil
	}
	if materialPreview, isResolved := toolCatalogBuilder.filePreviewResolvedMaterial(toolContext, input, handlerContext.request); isResolved {
		return materialPreview, nil
	}
	previewPath, materialFailure := toolCatalogBuilder.filePreviewPath(toolContext, input, handlerContext.request)
	if materialFailure != nil {
		return *materialFailure, nil
	}
	if cachedPreview, isCached := cachedFilePreviewResult(handlerContext.request.InputParts, previewPath); isCached {
		return fileToolSuccess(cachedPreview), nil
	}
	outcome, actorFailure := toolCatalogBuilder.runRequesterShell(toolContext, handlerContext.request, requesterShellCommand{
		Command:            fileReadShellCommand(previewPath, maximumFilePreviewBytes+1),
		OutputMaximumBytes: maximumFilePreviewBytes + fileReadShellOutputReserveBytes,
	})
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if outcome.RunError != nil {
		if fallbackPath, fallbackFailure, isFound := toolCatalogBuilder.filePreviewFallbackPath(toolContext, previewPath, handlerContext.request); isFound {
			if fallbackFailure != nil {
				return *fallbackFailure, nil
			}
			if strings.TrimSpace(fallbackPath) != "" && strings.TrimSpace(fallbackPath) != strings.TrimSpace(previewPath) {
				return toolCatalogBuilder.previewFileTool(toolContext, filePreviewToolInput{Path: fallbackPath}, handlerContext)
			}
		}
		return outcome.toolFailure("read_file", "file_preview", previewPath), nil
	}
	sizeBytes, content, isParsed := parseFileReadShellOutput(outcome.CommandResult.Stdout)
	if !isParsed {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "file_preview", "file size probe returned unreadable output"), nil
	}
	contentType := previewContentType(previewPath)
	if strings.HasPrefix(contentType, "image/") {
		return fileToolSuccess(filePreviewResult(previewPath, contentType, sizeBytes, "", "image", "use the image input part or image_read for visual inspection")), nil
	}
	if toolCatalogBuilder.capabilityClient.HTTPClient != nil {
		bridgePath := toolCatalogBuilder.capabilityBridgePath(handlerContext.request, previewPath)
		if result, isConverted := toolCatalogBuilder.convertFilePreviewWithCapability(toolContext, handlerContext.request, previewPath, bridgePath, contentType, sizeBytes); isConverted {
			return result, nil
		}
	}
	return filePreviewFromShellContent(previewPath, contentType, sizeBytes, content), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) filePreviewResolvedMaterial(toolContext context.Context, input filePreviewToolInput, request ToolCatalogRequest) (toolcontract.ToolResult, bool) {
	if strings.TrimSpace(input.Path) != "" || strings.TrimSpace(input.MaterialID) == "" {
		return toolcontract.ToolResult{}, false
	}
	material, errorValue := resolveReadableAttachmentMaterial(toolContext, request, input.MaterialID)
	if errorValue != nil {
		return attachmentResolutionFailure("file_preview", errorValue), true
	}
	if attachmentMaterialLooksLikeImage(material) {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_preview", "attachment material is an image; use image_read"), true
	}
	if result, hasPreview := filePreviewResultFromVisibleMaterial(material); hasPreview {
		return fileToolSuccess(result), true
	}
	return toolcontract.ToolResult{}, false
}

func filePreviewResultFromVisibleMaterial(material agentcontract.VisibleContextMaterial) (map[string]any, bool) {
	preview := strings.TrimSpace(material.MarkdownPreview)
	status := strings.TrimSpace(material.ConversionStatus)
	message := strings.TrimSpace(material.ConversionMessage)
	if preview == "" && status == "" && message == "" {
		return nil, false
	}
	contentType := firstNonEmptyString(strings.TrimSpace(material.ContentType), previewContentType(material.Path))
	return filePreviewResult(material.Path, contentType, material.SizeBytes, preview, status, message), true
}

func (toolCatalogBuilder *ToolCatalogBuilder) filePreviewFallbackPath(toolContext context.Context, path string, request ToolCatalogRequest) (string, *toolcontract.ToolResult, bool) {
	material, isFound := visibleAttachmentMaterialForPath(request.VisibleContext, path)
	if !isFound {
		return "", nil, false
	}
	materialID := strings.TrimSpace(material.MaterialID)
	if materialID == "" {
		return "", nil, false
	}
	resolvedMaterial, errorValue := resolveReadableAttachmentMaterial(toolContext, request, materialID)
	if errorValue != nil {
		result := attachmentResolutionFailure("file_preview", errorValue)
		return "", &result, true
	}
	if attachmentMaterialLooksLikeImage(resolvedMaterial) {
		result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_preview", "attachment material is an image; use image_read")
		return "", &result, true
	}
	if previewResult, hasPreview := filePreviewResultFromVisibleMaterial(resolvedMaterial); hasPreview {
		result := fileToolSuccess(previewResult)
		return "", &result, true
	}
	return toolCatalogBuilder.resolveAgentWorkspacePath(resolvedMaterial.Path), nil, true
}

func visibleAttachmentMaterialForPath(visibleContext agentcontract.VisibleContext, path string) (agentcontract.VisibleContextMaterial, bool) {
	candidates := visibleAttachmentMaterials(visibleContext)
	if material, isFound := visibleAttachmentMaterialWithExactPath(candidates, path); isFound {
		return material, true
	}
	return visibleAttachmentMaterialWithFilename(candidates, filepath.Base(strings.TrimSpace(path)))
}

func visibleAttachmentMaterials(visibleContext agentcontract.VisibleContext) []agentcontract.VisibleContextMaterial {
	materials := append([]agentcontract.VisibleContextMaterial{}, visibleContext.CurrentMaterials...)
	materials = append(materials, visibleContext.Materials...)
	for _, message := range visibleContext.Messages {
		materials = append(materials, message.Materials...)
	}
	return uniqueVisibleAttachmentMaterials(materials)
}

func uniqueVisibleAttachmentMaterials(materials []agentcontract.VisibleContextMaterial) []agentcontract.VisibleContextMaterial {
	seen := map[string]bool{}
	result := make([]agentcontract.VisibleContextMaterial, 0, len(materials))
	for _, material := range materials {
		key := visibleAttachmentMaterialKey(material)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, material)
	}
	return result
}

func visibleAttachmentMaterialKey(material agentcontract.VisibleContextMaterial) string {
	if materialID := strings.TrimSpace(material.MaterialID); materialID != "" {
		return "material:" + materialID
	}
	if path := strings.TrimSpace(material.Path); path != "" {
		return "path:" + path
	}
	if filename := strings.TrimSpace(material.Filename); filename != "" {
		return "filename:" + filename
	}
	return ""
}

func visibleAttachmentMaterialWithExactPath(materials []agentcontract.VisibleContextMaterial, path string) (agentcontract.VisibleContextMaterial, bool) {
	trimmedPath := strings.TrimSpace(path)
	for _, material := range materials {
		if strings.TrimSpace(material.Path) == trimmedPath {
			return material, true
		}
	}
	return agentcontract.VisibleContextMaterial{}, false
}

func visibleAttachmentMaterialWithFilename(materials []agentcontract.VisibleContextMaterial, filename string) (agentcontract.VisibleContextMaterial, bool) {
	trimmedFilename := strings.TrimSpace(filename)
	if trimmedFilename == "" || trimmedFilename == "." {
		return agentcontract.VisibleContextMaterial{}, false
	}
	matches := []agentcontract.VisibleContextMaterial{}
	for _, material := range materials {
		if strings.TrimSpace(material.Filename) == trimmedFilename || filepath.Base(strings.TrimSpace(material.Path)) == trimmedFilename {
			matches = append(matches, material)
		}
	}
	if len(matches) != 1 {
		return agentcontract.VisibleContextMaterial{}, false
	}
	return matches[0], true
}

func (toolCatalogBuilder *ToolCatalogBuilder) filePreviewPath(toolContext context.Context, input filePreviewToolInput, request ToolCatalogRequest) (string, *toolcontract.ToolResult) {
	path := strings.TrimSpace(input.Path)
	materialID := strings.TrimSpace(input.MaterialID)
	if path != "" {
		return path, nil
	}
	if materialID == "" {
		result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_preview", "path or materialID is required")
		return "", &result
	}
	material, errorValue := resolveReadableAttachmentMaterial(toolContext, request, materialID)
	if errorValue != nil {
		result := attachmentResolutionFailure("file_preview", errorValue)
		return "", &result
	}
	if attachmentMaterialLooksLikeImage(material) {
		result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_preview", "attachment material is an image; use image_read")
		return "", &result
	}
	return toolCatalogBuilder.resolveAgentWorkspacePath(material.Path), nil
}

func attachmentMaterialLooksLikeImage(material agentcontract.VisibleContextMaterial) bool {
	contentType := strings.ToLower(strings.TrimSpace(material.ContentType))
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	filename := strings.ToLower(strings.TrimSpace(material.Filename))
	return strings.HasSuffix(filename, ".png") ||
		strings.HasSuffix(filename, ".jpg") ||
		strings.HasSuffix(filename, ".jpeg") ||
		strings.HasSuffix(filename, ".gif") ||
		strings.HasSuffix(filename, ".webp")
}

func cachedFilePreviewResultForInput(parts []agentcontract.AgentPart, input filePreviewToolInput) (map[string]any, bool) {
	if materialID := strings.TrimSpace(input.MaterialID); materialID != "" {
		return cachedFilePreviewResultByMaterialID(parts, materialID)
	}
	return cachedFilePreviewResult(parts, strings.TrimSpace(input.Path))
}

func cachedFilePreviewResultByMaterialID(parts []agentcontract.AgentPart, materialID string) (map[string]any, bool) {
	trimmedMaterialID := strings.TrimSpace(materialID)
	for _, part := range parts {
		if agentPartMaterialID(part) != trimmedMaterialID {
			continue
		}
		return cachedFilePreviewResultFromPart(part)
	}
	return nil, false
}

func cachedFilePreviewResult(parts []agentcontract.AgentPart, path string) (map[string]any, bool) {
	for _, part := range parts {
		if part.File == nil || strings.TrimSpace(part.File.Path) != strings.TrimSpace(path) {
			continue
		}
		return cachedFilePreviewResultFromPart(part)
	}
	return nil, false
}

func cachedFilePreviewResultFromPart(part agentcontract.AgentPart) (map[string]any, bool) {
	if part.File == nil {
		return nil, false
	}
	if strings.TrimSpace(part.File.MarkdownPreview) == "" && strings.TrimSpace(part.File.ConversionStatus) == "" {
		return nil, false
	}
	return filePreviewResult(
		part.File.Path,
		part.File.ContentType,
		part.File.SizeBytes,
		part.File.MarkdownPreview,
		firstNonEmptyString(part.File.ConversionStatus, "cached"),
		part.File.ConversionMessage,
	), true
}

func agentPartMaterialID(part agentcontract.AgentPart) string {
	fileID := strings.TrimSpace(part.Source.FileID)
	if fileID == "" {
		return ""
	}
	return firstNonEmptyString(strings.TrimSpace(part.Source.Platform), "attachment") + ":" + fileID
}

func (toolCatalogBuilder *ToolCatalogBuilder) convertFilePreviewWithCapability(toolContext context.Context, request ToolCatalogRequest, path string, bridgePath string, contentType string, sizeBytes int64) (toolcontract.ToolResult, bool) {
	var response struct {
		Content      string          `json:"content"`
		IsError      bool            `json:"isError"`
		Status       string          `json:"status"`
		Message      string          `json:"message"`
		ErrorCode    string          `json:"errorCode"`
		FailureStage string          `json:"failureStage"`
		Result       json.RawMessage `json:"result"`
	}
	input := toolcontract.MarshalToolInput(map[string]any{"path": bridgePath, "maxOutputBytes": maximumFilePreviewBytes})
	requestDocument, errorValue := toolCatalogBuilder.capabilityRequestForOperation(toolContext, "document_read", request, input)
	if errorValue != nil {
		return toolcontract.ToolResult{}, false
	}
	errorValue = toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/document_read/invoke", requestDocument, &response)
	if errorValue != nil || response.IsError || response.Status == "error" || response.Status == "denied" {
		return toolcontract.ToolResult{}, false
	}
	var document struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
		Format    string `json:"format"`
	}
	if json.Unmarshal(response.Result, &document) != nil {
		document.Content = response.Content
	}
	conversionStatus := "converted"
	if document.Truncated {
		conversionStatus = "truncated"
	}
	result := filePreviewResult(path, contentType, sizeBytes, document.Content, conversionStatus, "")
	if strings.TrimSpace(document.Format) != "" {
		result["previewFormat"] = strings.TrimSpace(document.Format)
	}
	return fileToolSuccess(result), true
}

func filePreviewFromShellContent(path string, contentType string, sizeBytes int64, content string) toolcontract.ToolResult {
	isTruncated := len(content) > maximumFilePreviewBytes
	if isTruncated {
		content = content[:maximumFilePreviewBytes]
	}
	if !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return fileToolSuccess(filePreviewResult(path, contentType, sizeBytes, "", "unsupported", "file is not UTF-8 text and no converted preview is available"))
	}
	windowedContent, isContentTruncated := truncateTextByBytes(content, maximumFilePreviewBytes)
	conversionStatus := "converted"
	if isTruncated || isContentTruncated {
		conversionStatus = "truncated"
	}
	return fileToolSuccess(filePreviewResult(path, contentType, sizeBytes, windowedContent, conversionStatus, ""))
}

func filePreviewResult(path string, contentType string, sizeBytes int64, markdownPreview string, conversionStatus string, conversionMessage string) map[string]any {
	return map[string]any{
		"path":              strings.TrimSpace(path),
		"filename":          filepath.Base(strings.TrimSpace(path)),
		"contentType":       strings.TrimSpace(contentType),
		"sizeBytes":         sizeBytes,
		"previewFormat":     "markdown",
		"markdownPreview":   strings.TrimSpace(markdownPreview),
		"conversionStatus":  strings.TrimSpace(conversionStatus),
		"conversionMessage": strings.TrimSpace(conversionMessage),
	}
}

func previewContentType(path string) string {
	if contentType := mime.TypeByExtension(filepath.Ext(strings.TrimSpace(path))); strings.TrimSpace(contentType) != "" {
		return strings.TrimSpace(contentType)
	}
	return "application/octet-stream"
}

func truncateTextByBytes(content string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len([]byte(content)) <= maxBytes {
		return content, false
	}
	document := []byte(content)
	if maxBytes > len(document) {
		maxBytes = len(document)
	}
	truncatedDocument := document[:maxBytes]
	for len(truncatedDocument) > 0 && !utf8.Valid(truncatedDocument) {
		truncatedDocument = truncatedDocument[:len(truncatedDocument)-1]
	}
	return string(truncatedDocument), true
}

func (toolCatalogBuilder *ToolCatalogBuilder) patchFileTool(toolContext context.Context, input filePatchToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	if len(input.Edits) == 0 {
		return fileExactEditFailure("file_edit", "", -1, 0, "edits is required"), nil
	}
	if len(input.Edits) > 100 {
		return fileExactEditFailure("file_edit", "", -1, len(input.Edits), "too many edits; split the patch into smaller groups"), nil
	}
	patchState := newFilePatchState()
	for editIndex, edit := range input.Edits {
		if result := toolCatalogBuilder.validatePatchEdit(toolContext, handlerContext, patchState, edit, editIndex); result != nil {
			return *result, nil
		}
	}
	if result := toolCatalogBuilder.writePatchState(toolContext, handlerContext, patchState); result != nil {
		return *result, nil
	}
	return fileToolSuccess(map[string]any{
		"editedFiles": append([]string{}, patchState.pathOrder...),
		"editCount":   len(input.Edits),
	}), nil
}

type filePatchState struct {
	originalContents map[string]string
	currentContents  map[string]string
	pathOrder        []string
}

func newFilePatchState() *filePatchState {
	return &filePatchState{
		originalContents: map[string]string{},
		currentContents:  map[string]string{},
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) validatePatchEdit(toolContext context.Context, handlerContext toolHandlerContext, patchState *filePatchState, edit filePatchEditInput, editIndex int) *toolcontract.ToolResult {
	path := strings.TrimSpace(edit.Path)
	if path == "" {
		result := fileExactEditFailure("file_edit", "", editIndex, 0, "path is required")
		return &result
	}
	if edit.OldText == "" {
		result := fileExactEditFailure("file_edit", path, editIndex, 0, "oldText is required")
		return &result
	}
	if result := toolCatalogBuilder.loadEditableFile(toolContext, handlerContext, patchState, path); result != nil {
		return result
	}
	currentContent := patchState.currentContents[path]
	updatedContent, matchCount, applied := applyExactOrTolerantEdit(currentContent, edit.OldText, edit.NewText)
	if !applied {
		result := fileExactEditFailure("file_edit", path, editIndex, matchCount, fileEditMatchFailureGuidance(currentContent, edit.OldText, matchCount))
		return &result
	}
	patchState.currentContents[path] = updatedContent
	return nil
}

func applyExactOrTolerantEdit(content string, oldText string, newText string) (string, int, bool) {
	if oldText == "" {
		return "", 0, false
	}
	if count := strings.Count(content, oldText); count > 1 {
		return "", count, false
	} else if count == 1 {
		return strings.Replace(content, oldText, newText, 1), 1, true
	}
	if updated, ok := replacePartWithMissingLeadingWhitespace(content, oldText, newText); ok {
		return updated, 1, true
	}
	if updated, count, ok := applyUnicodeNormalizedEdit(content, oldText, newText); ok {
		return updated, count, true
	} else if count > 1 {
		return "", count, false
	}
	if updated, count, ok := applyWhitespaceTolerantEdit(content, oldText, newText); ok {
		return updated, count, true
	} else if count > 1 {
		return "", count, false
	}
	return "", 0, false
}

// The final match layer tolerates inline whitespace drift the way apply_patch
// does: runs of spaces and tabs collapse to one space in both the file and
// oldText, so text that only differs by internal spacing still matches. The
// match must still be unique, and its collapsed span is mapped back to the exact
// original bytes so the replacement is byte-precise. Newlines are never
// collapsed, so line structure still anchors the match.
func applyWhitespaceTolerantEdit(content string, oldText string, newText string) (string, int, bool) {
	normalizedOldText, _ := collapseInlineWhitespace(oldText)
	if strings.TrimSpace(normalizedOldText) == "" {
		return "", 0, false
	}
	normalizedContent, originalOffsets := collapseInlineWhitespace(content)
	count := strings.Count(normalizedContent, normalizedOldText)
	if count != 1 {
		return "", count, false
	}
	normalizedStart := strings.Index(normalizedContent, normalizedOldText)
	normalizedEnd := normalizedStart + len(normalizedOldText)
	return content[:originalOffsets[normalizedStart]] + newText + content[originalOffsets[normalizedEnd]:], 1, true
}

// collapseInlineWhitespace collapses each run of ASCII spaces and tabs to a
// single space and returns the normalized text plus a byte offset for every
// normalized position (including the end), mapping normalized bytes back to the
// original. Newlines and every other byte pass through unchanged.
func collapseInlineWhitespace(value string) (string, []int) {
	var builder strings.Builder
	offsets := make([]int, 0, len(value)+1)
	inWhitespaceRun := false
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == ' ' || character == '\t' {
			if inWhitespaceRun {
				continue
			}
			inWhitespaceRun = true
			offsets = append(offsets, index)
			builder.WriteByte(' ')
			continue
		}
		inWhitespaceRun = false
		offsets = append(offsets, index)
		builder.WriteByte(character)
	}
	offsets = append(offsets, len(value))
	return builder.String(), offsets
}

func replacePartWithMissingLeadingWhitespace(content string, oldText string, newText string) (string, bool) {
	fileLines := strings.Split(content, "\n")
	partLines := strings.Split(oldText, "\n")
	replaceLines := strings.Split(newText, "\n")
	if len(partLines) == 0 || len(partLines) > len(fileLines) {
		return "", false
	}
	if minLead := minCommonLeadingWhitespace(append(append([]string{}, partLines...), replaceLines...)); minLead > 0 {
		partLines = outdentLines(partLines, minLead)
		replaceLines = outdentLines(replaceLines, minLead)
	}
	patternLength := len(partLines)
	matchStartIndexes := []int{}
	addedPrefix := ""
	for index := 0; index+patternLength <= len(fileLines); index++ {
		if prefix, ok := matchButForLeadingWhitespace(fileLines[index:index+patternLength], partLines); ok {
			matchStartIndexes = append(matchStartIndexes, index)
			addedPrefix = prefix
		}
	}
	if len(matchStartIndexes) != 1 {
		return "", false
	}
	matchStart := matchStartIndexes[0]
	reindentedReplacement := make([]string, len(replaceLines))
	for index, line := range replaceLines {
		if strings.TrimSpace(line) == "" {
			reindentedReplacement[index] = line
		} else {
			reindentedReplacement[index] = addedPrefix + line
		}
	}
	updatedLines := append(append(append([]string{}, fileLines[:matchStart]...), reindentedReplacement...), fileLines[matchStart+patternLength:]...)
	return strings.Join(updatedLines, "\n"), true
}

func matchButForLeadingWhitespace(windowLines []string, patternLines []string) (string, bool) {
	if len(windowLines) != len(patternLines) {
		return "", false
	}
	window := rightTrimLines(windowLines)
	pattern := rightTrimLines(patternLines)
	for index := range window {
		if strings.TrimLeft(window[index], " \t") != strings.TrimLeft(pattern[index], " \t") {
			return "", false
		}
	}
	prefixes := map[string]bool{}
	for index := range window {
		if strings.TrimSpace(window[index]) == "" {
			continue
		}
		if len(window[index]) < len(pattern[index]) {
			return "", false
		}
		prefixes[window[index][:len(window[index])-len(pattern[index])]] = true
	}
	if len(prefixes) > 1 {
		return "", false
	}
	for prefix := range prefixes {
		return prefix, true
	}
	return "", true
}

func rightTrimLines(lines []string) []string {
	trimmed := make([]string, len(lines))
	for index, line := range lines {
		trimmed[index] = strings.TrimRight(line, " \t")
	}
	return trimmed
}

func applyUnicodeNormalizedEdit(content string, oldText string, newText string) (string, int, bool) {
	normalizedContent := normalizeEditLookalikes(content)
	normalizedOldText := normalizeEditLookalikes(oldText)
	count := strings.Count(normalizedContent, normalizedOldText)
	if count != 1 {
		return "", count, false
	}
	byteIndex := strings.Index(normalizedContent, normalizedOldText)
	runeIndex := utf8.RuneCountInString(normalizedContent[:byteIndex])
	oldTextRuneLength := utf8.RuneCountInString(normalizedOldText)
	contentRunes := []rune(content)
	updated := string(contentRunes[:runeIndex]) + newText + string(contentRunes[runeIndex+oldTextRuneLength:])
	return updated, 1, true
}

func normalizeEditLookalikes(value string) string {
	return strings.Map(func(character rune) rune {
		switch character {
		case '\u2018', '\u2019', '\u201A', '\u201B':
			return '\''
		case '\u201C', '\u201D', '\u201E', '\u201F':
			return '"'
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			return '-'
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			return ' '
		}
		return character
	}, value)
}

func minCommonLeadingWhitespace(lines []string) int {
	minimum := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		leading := len(line) - len(strings.TrimLeft(line, " \t"))
		if minimum == -1 || leading < minimum {
			minimum = leading
		}
	}
	if minimum < 0 {
		return 0
	}
	return minimum
}

func outdentLines(lines []string, amount int) []string {
	outdented := make([]string, len(lines))
	for index, line := range lines {
		if strings.TrimSpace(line) == "" || len(line) < amount {
			outdented[index] = line
			continue
		}
		outdented[index] = line[amount:]
	}
	return outdented
}

func fileEditMatchFailureGuidance(content string, oldText string, matchCount int) string {
	if matchCount > 1 {
		return "oldText matched " + strconv.Itoa(matchCount) + " places; include more surrounding lines so oldText identifies exactly one location."
	}
	if similar := closestFileLines(content, oldText); similar != "" {
		return "oldText was not found, even after allowing whitespace and quote differences. The closest current lines in the file are:\n" + similar + "\nCopy the exact text shown above into oldText and retry file.edit. Do not rewrite the whole file — that discards the rest of the work."
	}
	return "oldText was not found, even after allowing whitespace and quote differences. Read the file with file_read to copy the exact current text, then retry file.edit. Do not rewrite the whole file."
}

func closestFileLines(content string, oldText string) string {
	target := mostDistinctiveLine(oldText)
	if target == "" {
		return ""
	}
	contentLines := strings.Split(content, "\n")
	bestIndex := -1
	bestScore := 0.0
	for index, line := range contentLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if score := lineSimilarity(trimmed, target); score >= 0.6 && score > bestScore {
			bestScore = score
			bestIndex = index
		}
	}
	if bestIndex == -1 {
		return ""
	}
	windowStart := bestIndex - 2
	if windowStart < 0 {
		windowStart = 0
	}
	windowEnd := bestIndex + 4
	if windowEnd > len(contentLines) {
		windowEnd = len(contentLines)
	}
	return strings.Join(contentLines[windowStart:windowEnd], "\n")
}

func mostDistinctiveLine(oldText string) string {
	distinctive := ""
	for _, line := range strings.Split(oldText, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > len(distinctive) {
			distinctive = trimmed
		}
	}
	return distinctive
}

func lineSimilarity(first string, second string) float64 {
	firstBigrams := characterBigrams(first)
	secondBigrams := characterBigrams(second)
	if len(firstBigrams) == 0 || len(secondBigrams) == 0 {
		return 0
	}
	shared := 0
	for bigram := range firstBigrams {
		if secondBigrams[bigram] {
			shared++
		}
	}
	return 2 * float64(shared) / float64(len(firstBigrams)+len(secondBigrams))
}

func characterBigrams(value string) map[string]bool {
	runes := []rune(value)
	bigrams := map[string]bool{}
	for index := 0; index+1 < len(runes); index++ {
		bigrams[string(runes[index:index+2])] = true
	}
	return bigrams
}

func (toolCatalogBuilder *ToolCatalogBuilder) loadEditableFile(toolContext context.Context, handlerContext toolHandlerContext, patchState *filePatchState, path string) *toolcontract.ToolResult {
	if _, isLoaded := patchState.currentContents[path]; isLoaded {
		return nil
	}
	outcome, actorFailure := toolCatalogBuilder.runRequesterShell(toolContext, handlerContext.request, requesterShellCommand{
		Command:            fileReadShellCommand(path, maximumEditableTextFileBytes+1),
		OutputMaximumBytes: maximumEditableTextFileBytes + fileReadShellOutputReserveBytes,
	})
	if actorFailure != nil {
		return actorFailure
	}
	if outcome.RunError != nil {
		result := outcome.toolFailure("read_file", "file_edit", path)
		return &result
	}
	sizeBytes, content, isParsed := parseFileReadShellOutput(outcome.CommandResult.Stdout)
	if !isParsed {
		result := toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "file_edit", "file size probe returned unreadable output")
		return &result
	}
	if sizeBytes > int64(maximumEditableTextFileBytes) || len(content) > maximumEditableTextFileBytes {
		result := fileExactEditFailure("file_edit", path, -1, 0, "file is too large for exact edit; rewrite a smaller generated file or use a more specific workflow")
		return &result
	}
	if !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_edit", "file_edit supports UTF-8 text files; use a specialized document or artifact tool for binary files")
		return &result
	}
	patchState.originalContents[path] = content
	patchState.currentContents[path] = content
	patchState.pathOrder = append(patchState.pathOrder, path)
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) writePatchState(toolContext context.Context, handlerContext toolHandlerContext, patchState *filePatchState) *toolcontract.ToolResult {
	writtenPaths := []string{}
	for _, path := range patchState.pathOrder {
		if failureResult := toolCatalogBuilder.runRequesterFileWrite(toolContext, handlerContext, "file_edit", path, patchState.currentContents[path]); failureResult != nil {
			toolCatalogBuilder.rollbackPatchWrites(toolContext, handlerContext, patchState, writtenPaths)
			return failureResult
		}
		writtenPaths = append(writtenPaths, path)
	}
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) rollbackPatchWrites(toolContext context.Context, handlerContext toolHandlerContext, patchState *filePatchState, writtenPaths []string) {
	for _, path := range writtenPaths {
		_ = toolCatalogBuilder.runRequesterFileWrite(toolContext, handlerContext, "file_edit", path, patchState.originalContents[path])
	}
}

func fileExactEditFailure(stage string, path string, editIndex int, matchCount int, guidance string) toolcontract.ToolResult {
	content := marshalToolResult(map[string]any{
		"path":       strings.TrimSpace(path),
		"editIndex":  editIndex,
		"matchCount": matchCount,
		"guidance":   strings.TrimSpace(guidance),
	})
	result := toolcontract.ToolFailureWithOutput(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, stage, content, json.RawMessage(content))
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	result.Failure.RetryPolicy = "different_input"
	result.Failure.RecoveryHints = []toolcontract.RecoveryHint{{
		Action:    "inspect_then_targeted_edit",
		ToolNames: []string{"file_read", "file_edit"},
		Reason:    "The oldText no longer matches the file on disk. Read the current file with file_read, copy the exact snippet, then retry a targeted file.edit. Do not rewrite the whole file — a targeted edit keeps the rest of the work and is far cheaper.",
	}}
	return result
}

func (toolCatalogBuilder *ToolCatalogBuilder) attachFileTool(toolContext context.Context, input fileAttachToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	attachmentInputs := normalizeFileAttachInputs(input)
	if len(attachmentInputs) == 0 {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_deliver", "files must contain at least one path"), nil
	}
	attachments := []toolcontract.FileAttachment{}
	deliveredPaths := []string{}
	for _, attachmentInput := range attachmentInputs {
		attachment, failureResult := toolCatalogBuilder.fileAttachment(toolContext, attachmentInput, handlerContext)
		if failureResult != nil {
			return *failureResult, nil
		}
		attachments = append(attachments, attachment)
		deliveredPaths = append(deliveredPaths, attachment.DevicePath)
	}
	data := json.RawMessage(marshalToolResult(map[string]any{
		"deliveredPaths":  deliveredPaths,
		"attachmentCount": len(attachments),
	}))
	return toolcontract.ToolResult{
		Output:      toolcontract.ToolOutput{Content: "files delivered", Data: data},
		Attachments: attachments,
	}, nil
}

func normalizeFileAttachInputs(input fileAttachToolInput) []fileAttachFileInput {
	if len(input.Files) > 0 {
		return input.Files
	}
	if strings.TrimSpace(input.Path) == "" {
		return nil
	}
	return []fileAttachFileInput{{
		Path:        input.Path,
		Filename:    input.Filename,
		ContentType: input.ContentType,
		Title:       input.Title,
	}}
}

func (toolCatalogBuilder *ToolCatalogBuilder) fileAttachment(toolContext context.Context, input fileAttachFileInput, handlerContext toolHandlerContext) (toolcontract.FileAttachment, *toolcontract.ToolResult) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_deliver", "delivery path is required")
		return toolcontract.FileAttachment{}, &result
	}
	outcome, actorFailure := toolCatalogBuilder.runRequesterShell(toolContext, handlerContext.request, requesterShellCommand{
		Command:            fileReadShellCommand(path, inlineAttachmentMaximumBytes+1),
		OutputMaximumBytes: inlineAttachmentMaximumBytes + fileReadShellOutputReserveBytes,
	})
	if actorFailure != nil {
		return toolcontract.FileAttachment{}, actorFailure
	}
	if outcome.RunError != nil {
		result := toolCatalogBuilder.fileDeliverReadFailure(toolContext, handlerContext, path, outcome)
		return toolcontract.FileAttachment{}, &result
	}
	sizeBytes, content, isParsed := parseFileReadShellOutput(outcome.CommandResult.Stdout)
	if !isParsed {
		result := toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "file_deliver", "file size probe returned unreadable output")
		return toolcontract.FileAttachment{}, &result
	}
	if sizeBytes > inlineAttachmentMaximumBytes || len(content) > inlineAttachmentMaximumBytes {
		result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_deliver", "file is too large to deliver as an inline attachment")
		return toolcontract.FileAttachment{}, &result
	}
	concretePath := toolCatalogBuilder.nativeRequesterPath(handlerContext.request, path)
	filename := attachmentFilename(input, concretePath)
	toolCatalogBuilder.persistDeliveredDocument(toolContext, handlerContext, path, filename, content)
	contentType := firstNonEmptyString(input.ContentType, mime.TypeByExtension(filepath.Ext(filename)), "application/octet-stream")
	return toolcontract.FileAttachment{
		DevicePath:    toolCatalogBuilder.agentWorkspacePath(concretePath),
		Filename:      filename,
		ContentType:   contentType,
		SizeBytes:     sizeBytes,
		Title:         strings.TrimSpace(input.Title),
		ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)),
	}, nil
}

const fileDeliverCandidateFileLimit = 8

// A not_found read failure otherwise forces the model to guess a corrected path across
// several retries. Listing what actually exists nearby lets it recover in one step.
func (toolCatalogBuilder *ToolCatalogBuilder) fileDeliverReadFailure(toolContext context.Context, handlerContext toolHandlerContext, path string, outcome requesterShellOutcome) toolcontract.ToolResult {
	result := outcome.toolFailure("read_file", "file_deliver", path)
	if outcome.failureCode() != security.ActorErrorCodeNotFound {
		return result
	}
	candidateFiles := toolCatalogBuilder.fileDeliverCandidateFiles(toolContext, handlerContext, path)
	if len(candidateFiles) == 0 {
		return result
	}
	dataFields := actorFailureDataFields("read_file", "file_deliver", path, outcome.actorError("read_file", path))
	dataFields["candidateFiles"] = candidateFiles
	result.Output.Data = json.RawMessage(marshalToolResult(dataFields))
	return result
}

func (toolCatalogBuilder *ToolCatalogBuilder) fileDeliverCandidateFiles(toolContext context.Context, handlerContext toolHandlerContext, path string) []string {
	candidateFiles := []string{}
	for _, directoryPath := range toolCatalogBuilder.fileDeliverCandidateDirectories(handlerContext.request, path) {
		for _, filename := range toolCatalogBuilder.directoryEntryNames(toolContext, handlerContext, directoryPath) {
			candidatePath := filepath.ToSlash(filepath.Join(directoryPath, filename))
			if stringSliceContains(candidateFiles, candidatePath) {
				continue
			}
			candidateFiles = append(candidateFiles, candidatePath)
			if len(candidateFiles) >= fileDeliverCandidateFileLimit {
				return candidateFiles
			}
		}
	}
	return candidateFiles
}

const deliveredDocumentsDirectoryPath = "~/documents"

func (toolCatalogBuilder *ToolCatalogBuilder) fileDeliverCandidateDirectories(request ToolCatalogRequest, path string) []string {
	requestedDirectoryPath := filepath.ToSlash(filepath.Dir(strings.TrimSpace(path)))
	directoryPaths := []string{requestedDirectoryPath}
	if toolCatalogBuilder.nativeRequesterPath(request, deliveredDocumentsDirectoryPath) != toolCatalogBuilder.nativeRequesterPath(request, requestedDirectoryPath) {
		directoryPaths = append(directoryPaths, deliveredDocumentsDirectoryPath)
	}
	return directoryPaths
}

func (toolCatalogBuilder *ToolCatalogBuilder) directoryEntryNames(toolContext context.Context, handlerContext toolHandlerContext, directoryPath string) []string {
	outcome, actorFailure := toolCatalogBuilder.runRequesterShell(toolContext, handlerContext.request, requesterShellCommand{
		Command:       "ls -1A -- " + shellPathArgument(directoryPath),
		TimeoutSecond: 5,
	})
	if actorFailure != nil || outcome.RunError != nil || outcome.CommandResult.ExitCode != 0 {
		return nil
	}
	filenames := []string{}
	for _, line := range strings.Split(outcome.CommandResult.Stdout, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine != "" {
			filenames = append(filenames, trimmedLine)
		}
	}
	return filenames
}

func shellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func stringSliceContains(values []string, value string) bool {
	for _, existingValue := range values {
		if existingValue == value {
			return true
		}
	}
	return false
}

// A delivered document is copied into the requester's ~/documents so a later edit or
// delete task finds it by name, independent of where the model built it. Best effort:
// a copy failure never blocks the delivery itself.
func (toolCatalogBuilder *ToolCatalogBuilder) persistDeliveredDocument(toolContext context.Context, handlerContext toolHandlerContext, sourcePath string, filename string, content string) {
	if !isPersistableDocumentFilename(filename) {
		return
	}
	destinationPath := deliveredDocumentsDirectoryPath + "/" + filename
	if toolCatalogBuilder.nativeRequesterPath(handlerContext.request, destinationPath) == toolCatalogBuilder.nativeRequesterPath(handlerContext.request, sourcePath) {
		return
	}
	_ = toolCatalogBuilder.runRequesterFileWrite(toolContext, handlerContext, "file_deliver", destinationPath, content)
}

func isPersistableDocumentFilename(filename string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".docx", ".doc", ".pdf", ".xlsx", ".xlsm", ".xls", ".pptx", ".ppt", ".csv":
		return true
	default:
		return false
	}
}

func attachmentFilename(input fileAttachFileInput, resolvedPath string) string {
	if strings.TrimSpace(input.Filename) != "" {
		return strings.TrimSpace(input.Filename)
	}
	return filepath.Base(resolvedPath)
}

func attachmentResolutionFailure(stage string, errorValue error) toolcontract.ToolResult {
	summary := strings.TrimSpace(errorValue.Error()) + ". This attachment reference is not openable in the current conversation. If it is a file you created or saved earlier, it persists as a workspace file: find it by name (for example `ls ~/documents`) and open that workspace path with file_read or file_preview, or edit it with your document skill's script. Only if no such workspace file exists, summarize from the conversation or tell the user it could not be opened."
	return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, stage, summary)
}

func resolveReadableAttachmentMaterial(toolContext context.Context, request ToolCatalogRequest, materialID string) (agentcontract.VisibleContextMaterial, error) {
	if request.AttachmentMaterialResolver == nil {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment material resolver is unavailable")
	}
	material, errorValue := request.AttachmentMaterialResolver.ResolveAttachmentMaterial(toolContext, materialID)
	if errorValue != nil {
		return agentcontract.VisibleContextMaterial{}, errorValue
	}
	if strings.TrimSpace(material.Path) == "" {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment material has no readable workspace path")
	}
	return material, nil
}

func validateAttachmentMaterialTool(toolName string, material agentcontract.VisibleContextMaterial) error {
	contentType := strings.ToLower(strings.TrimSpace(material.ContentType))
	switch strings.TrimSpace(toolName) {
	case "image_read":
		if contentType != "" && !strings.HasPrefix(contentType, "image/") {
			return errors.New("attachment material is not an image; use document_read")
		}
	case "document_read":
		if strings.HasPrefix(contentType, "image/") {
			return errors.New("attachment material is an image; use image_read")
		}
	}
	return nil
}
