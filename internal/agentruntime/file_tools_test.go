package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func assertFileResourceEffect(t *testing.T, result toolcontract.ToolResult, objectType string, effect string, path string) {
	t.Helper()
	for _, resourceEffect := range result.Effects {
		if resourceEffect.ObjectType == objectType && resourceEffect.Effect == effect && resourceEffect.Path == path {
			return
		}
	}
	t.Fatalf("missing %s %s effect for %s in %+v", objectType, effect, path, result.Effects)
}

func assertStringArrayResultField(t *testing.T, result toolcontract.ToolResult, fieldName string, expected []string) {
	t.Helper()
	var document map[string]any
	if errorValue := json.Unmarshal(result.Output.Data, &document); errorValue != nil {
		t.Fatalf("invalid result data: %v", errorValue)
	}
	values, isArray := document[fieldName].([]any)
	if !isArray || len(values) != len(expected) {
		t.Fatalf("expected %s=%+v, got %+v", fieldName, expected, document[fieldName])
	}
	for index, expectedValue := range expected {
		if values[index] != expectedValue {
			t.Fatalf("expected %s[%d]=%q, got %+v", fieldName, index, expectedValue, values[index])
		}
	}
}

func TestFileAttachToolAttachesSinglePath(t *testing.T) {
	workspacePath := t.TempDir()
	requesterDeckPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.pptx"), "pptx")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.pdf"), "%PDF")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.html"), "<html></html>")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck-notes.txt"), "notes")

	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input: toolcontract.MarshalToolInput(map[string]any{
			"path": "tmp/deck/deck.pptx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected successful attachment result, got %s", result.ContentText())
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected one attachment, got %+v", result.Attachments)
	}
	if result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected attachment filenames to match paths, got %+v", result.Attachments)
	}
	expectedPath := "/workspace/private/people/person-1/tmp/deck/deck.pptx"
	assertFileResourceEffect(t, result, "file", "attached", expectedPath)
	assertStringArrayResultField(t, result, "deliveredPaths", []string{expectedPath})
}

func TestFileAttachToolAttachesMultipleFiles(t *testing.T) {
	workspacePath := t.TempDir()
	requesterDeckPath := filepath.Join(workspacePath, "private", "people", "person-1", "artifacts", "deck")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.html"), "<html></html>")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.pptx"), "pptx")

	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input: toolcontract.MarshalToolInput(map[string]any{
			"files": []map[string]string{
				{"path": "artifacts/deck/deck.html", "contentType": "text/html"},
				{"path": "artifacts/deck/deck.pptx", "contentType": "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected successful attachment result, got %s", result.ContentText())
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected two attachments, got %+v", result.Attachments)
	}
	if result.Attachments[0].Filename != "deck.html" || result.Attachments[1].Filename != "deck.pptx" {
		t.Fatalf("expected attachment filenames to match paths, got %+v", result.Attachments)
	}
	expectedPaths := []string{
		"/workspace/private/people/person-1/artifacts/deck/deck.html",
		"/workspace/private/people/person-1/artifacts/deck/deck.pptx",
	}
	assertStringArrayResultField(t, result, "deliveredPaths", expectedPaths)
	for _, path := range expectedPaths {
		assertFileResourceEffect(t, result, "file", "attached", path)
	}
}

func TestFileToolsAcceptVirtualHomePathsWithoutLeakingHostPath(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "projects/deck/presentation.md",
			"content": "# Deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file_write success, got %s", writeResult.ContentText())
	}
	if strings.Contains(writeResult.ContentText(), workspacePath) {
		t.Fatalf("expected file_write result not to expose host path, got %s", writeResult.ContentText())
	}
	expectedWrittenPath := "projects/deck/presentation.md"
	assertFileResourceEffect(t, writeResult, "file", "created", expectedWrittenPath)
	assertFileResourceEffect(t, writeResult, "workspace", "modified", expectedWrittenPath)
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "private", "people", "person-1", "projects", "deck", "presentation.md")); errorValue != nil {
		t.Fatal(errorValue)
	}

	attachResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": "projects/deck/presentation.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if attachResult.Failed() {
		t.Fatalf("expected file_deliver success, got %s", attachResult.ContentText())
	}
	expectedDevicePath := "/workspace/private/people/person-1/projects/deck/presentation.md"
	if attachResult.Attachments[0].DevicePath != expectedDevicePath {
		t.Fatalf("expected agent workspace device path, got %+v", attachResult.Attachments[0])
	}
}

func TestFileDeliverAcceptsVirtualHomePathReturnedByFileRead(t *testing.T) {
	workspacePath := t.TempDir()
	inboxFilePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "conv-1", "customer-support-weekly-check.json")
	writeTestFile(t, inboxFilePath, `{"status":"ok"}`)

	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": "~/inbox/mattermost/conv-1/customer-support-weekly-check.json",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() {
		t.Fatalf("expected file_read success for ~/ path, got %s", readResult.ContentText())
	}
	var readDocument map[string]any
	if errorValue := json.Unmarshal(readResult.Output.Data, &readDocument); errorValue != nil {
		t.Fatalf("invalid file_read result data: %v", errorValue)
	}
	returnedPath, isString := readDocument["path"].(string)
	if !isString || returnedPath == "" {
		t.Fatalf("expected file_read to return a path, got %+v", readDocument)
	}

	deliverResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input:    toolcontract.MarshalToolInput(map[string]string{"path": returnedPath}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if deliverResult.Failed() {
		t.Fatalf("expected file_deliver to accept the exact path file_read returned (%q), got %s", returnedPath, deliverResult.ContentText())
	}
	if len(deliverResult.Attachments) != 1 || deliverResult.Attachments[0].Filename != "customer-support-weekly-check.json" {
		t.Fatalf("expected the inbox file attachment, got %+v", deliverResult.Attachments)
	}
}

func TestFileReadResolvesSiteRelativePathNativelyAndFailsAsNotFound(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": "app/src/App.tsx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.NotFound.String() {
		t.Fatalf("expected native shell resolution to report the missing file as not_found without a Go-side path filter, got %+v", result)
	}
}

func TestFileReadTreatsMissingSiteControlFileAsOptionalState(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseOptionalFileReadPathSuffixes([]string{".internkim/site.json", ".internkim/artifact-brief.md"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": "~/sites/site-1/.internkim/artifact-brief.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected missing optional site control file to be state, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), `"exists":false`) ||
		!strings.Contains(result.ContentText(), `"optional":true`) ||
		!strings.Contains(result.ContentText(), `"recommendedWritePath":"/workspace/circles/staff/sites/site-1/draft/.internkim/artifact-brief.md"`) {
		t.Fatalf("expected optional missing control-file payload, got %s", result.ContentText())
	}

	missingResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": "~/sites/site-1/app/src/App.tsx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !missingResult.Failed() || missingResult.FailureCode() != toolcontract.FailureCodes.NotFound.String() {
		t.Fatalf("expected ordinary missing file_read to fail as not_found, got %+v", missingResult)
	}
}

func TestFileWriteAcceptsPortablePathAndContent(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"path":    "projects/site/index.html",
			"content": "<html>ready</html>",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file_write success, got %s", writeResult.ContentText())
	}
	document, errorValue := os.ReadFile(filepath.Join(workspacePath, "private", "people", "person-1", "projects", "site", "index.html"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != "<html>ready</html>" {
		t.Fatalf("expected content to be written, got %q", string(document))
	}
}

func TestFileWriteDescribesContentAsExactFileBody(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolDefinition, isFound := toolRegistry.ToolDefinition("file_write")
	if !isFound {
		t.Fatal("expected file_write definition")
	}
	if !strings.Contains(toolDefinition.Description, "complete file body") {
		t.Fatalf("expected file_write description to explain exact file body, got %q", toolDefinition.Description)
	}
	if !strings.Contains(string(toolDefinition.InputSchema), "real line breaks") {
		t.Fatalf("expected file_write schema to explain multiline content, got %s", string(toolDefinition.InputSchema))
	}
	if !strings.Contains(toolDefinition.RecoveryCard.AvoidWhen, "escaped newline sequences") {
		t.Fatalf("expected file_write recovery card to warn about escaped newlines, got %+v", toolDefinition.RecoveryCard)
	}
}

func TestAttachmentResolutionFailureRedirectsToWorkspaceFile(t *testing.T) {
	result := attachmentResolutionFailure("file_preview", errors.New("attachment material is not visible in this conversation"))
	if result.Failure == nil {
		t.Fatal("expected a failure result")
	}
	if result.Failure.RetryPolicy == "no_retry" || result.Failure.FailureClass == "permanent" {
		t.Fatalf("attachment failure must stay recoverable so the model can open the workspace file, got policy=%q class=%q", result.Failure.RetryPolicy, result.Failure.FailureClass)
	}
	for _, expected := range []string{"workspace file", "file_read"} {
		if !strings.Contains(result.Failure.UserSafeSummary, expected) {
			t.Fatalf("expected recovery guidance %q in summary, got %q", expected, result.Failure.UserSafeSummary)
		}
	}
	if strings.Contains(result.Failure.UserSafeSummary, "do not retry") {
		t.Fatalf("attachment failure must not tell the model to give up, got %q", result.Failure.UserSafeSummary)
	}
}

func TestFileReadResultByteWindowPaginates(t *testing.T) {
	content := "abcdefghij"
	result := fileReadResult(content, fileReadToolInput{StartByte: 3}, 4)
	if result.Content != "defg" {
		t.Fatalf("content = %q", result.Content)
	}
	if result.StartByte != 3 || result.EndByte != 7 || result.NextByte != 7 || result.TotalBytes != 10 {
		t.Fatalf("byte metadata = %+v", result)
	}
	if result.IsEndOfFile {
		t.Fatal("expected more content after window")
	}
	final := fileReadResult(content, fileReadToolInput{StartByte: result.NextByte}, 100)
	if final.Content != "hij" || final.NextByte != 0 || !final.IsEndOfFile {
		t.Fatalf("final window = %+v", final)
	}
}

func TestFileReadResultByteWindowSnapsRuneBoundary(t *testing.T) {
	content := "aabc"
	result := fileReadResult(content, fileReadToolInput{StartByte: 2}, 4)
	if !utf8.ValidString(result.Content) {
		t.Fatalf("expected valid UTF-8 window, got %q", result.Content)
	}
}

func TestFileReadResultLineOverrunReturnsByteHint(t *testing.T) {
	content := "line1\nline2\n" + strings.Repeat("x", 5000)
	result := fileReadResult(content, fileReadToolInput{StartLine: 9}, 200)
	if result.Content != "" {
		t.Fatalf("expected empty content past last line, got %q", result.Content)
	}
	if !result.IsEndOfFile || !strings.Contains(result.ReadHint, "startByte") {
		t.Fatalf("expected end-of-file byte hint, got %+v", result)
	}
}

func TestFileReadReturnsLineRangeMetadata(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "source.ts")
	writeTestFile(t, filePath, "one\ntwo\nthree\nfour\n")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"path":      "tmp/source.ts",
			"startLine": 2,
			"lineCount": 2,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() {
		t.Fatalf("expected file_read success, got %s", readResult.ContentText())
	}
	resultData := map[string]any{}
	if errorValue := json.Unmarshal([]byte(readResult.ContentText()), &resultData); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultData["content"] != "two\nthree" || resultData["startLine"] != float64(2) || resultData["endLine"] != float64(3) || resultData["totalLines"] != float64(4) {
		t.Fatalf("expected line range metadata, got %+v", resultData)
	}
	if resultData["originalSizeBytes"] == nil || resultData["returnedBytes"] == nil || resultData["totalLinesKnown"] != true {
		t.Fatalf("expected honest size metadata, got %+v", resultData)
	}
}

func TestFileReadReturnsRangeAfterOldPrefixLimit(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "large-source.txt")
	lines := []string{}
	for index := 0; index < 3000; index++ {
		lines = append(lines, strings.Repeat("x", 80))
	}
	lines[2500] = "target-after-prefix"
	writeTestFile(t, filePath, strings.Join(lines, "\n"))
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"path":      "tmp/large-source.txt",
			"startLine": 2501,
			"lineCount": 1,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() {
		t.Fatalf("expected file_read success, got %s", readResult.ContentText())
	}
	if !strings.Contains(readResult.ContentText(), "target-after-prefix") {
		t.Fatalf("expected line beyond former prefix limit, got %s", readResult.ContentText())
	}
}

func TestFilePreviewReturnsTextPreview(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "source.html")
	writeTestFile(t, filePath, "<h1>Preview Title</h1>\n<p>Preview body</p>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_preview",
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": "tmp/source.html"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected file_preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "Preview Title") || !strings.Contains(previewResult.ContentText(), `"conversionStatus":"converted"`) {
		t.Fatalf("expected text preview, got %s", previewResult.ContentText())
	}
}

func TestFilePreviewUsesCachedAttachmentPreview(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "post-1", "report.pdf")
	writeTestFile(t, filePath, "%PDF")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		InputParts: []agentcontract.AgentPart{{
			Type: agentcontract.AgentPartTypeFile,
			File: &agentcontract.AgentFilePart{
				Path:             "/workspace/private/people/person-1/inbox/mattermost/post-1/report.pdf",
				Filename:         "report.pdf",
				ContentType:      "application/pdf",
				SizeBytes:        4,
				MarkdownPreview:  "# Cached report",
				ConversionStatus: "converted",
			},
		}},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_preview",
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": "/workspace/private/people/person-1/inbox/mattermost/post-1/report.pdf"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected cached file_preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "# Cached report") {
		t.Fatalf("expected cached preview, got %s", previewResult.ContentText())
	}
}

func TestFilePreviewUsesCachedAttachmentPreviewByMaterialID(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		InputParts: []agentcontract.AgentPart{{
			Type: agentcontract.AgentPartTypeFile,
			File: &agentcontract.AgentFilePart{
				Path:             "home/inbox/mattermost/post-1/report.html",
				Filename:         "report.html",
				ContentType:      "text/html",
				SizeBytes:        42,
				MarkdownPreview:  "# Cached material report",
				ConversionStatus: "converted",
			},
			Source: agentcontract.AgentPartSource{
				Platform:  "mattermost",
				MessageID: "post-1",
				FileID:    "file-1",
			},
		}},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_preview",
		Input:    toolcontract.MarshalToolInput(map[string]any{"materialID": "mattermost:file-1"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected cached material preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "# Cached material report") {
		t.Fatalf("expected cached material preview, got %s", previewResult.ContentText())
	}
}

func TestFileReadUsesCachedAttachmentPreviewWhenMaterialFileIsNotMounted(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		InputParts: []agentcontract.AgentPart{{
			Type: agentcontract.AgentPartTypeFile,
			File: &agentcontract.AgentFilePart{
				Path:             "home/inbox/mattermost/post-1/report.html",
				Filename:         "report.html",
				ContentType:      "text/html",
				SizeBytes:        42,
				MarkdownPreview:  "# Cached read report\n\nBody",
				ConversionStatus: "converted",
			},
			Source: agentcontract.AgentPartSource{
				Platform:  "mattermost",
				MessageID: "post-1",
				FileID:    "file-1",
			},
		}},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"path":      "home/inbox/mattermost/post-1/report.html",
			"startLine": 3,
			"lineCount": 1,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() {
		t.Fatalf("expected cached file_read success, got %s", readResult.ContentText())
	}
	if !strings.Contains(readResult.ContentText(), `"source":"attachmentPreview"`) || !strings.Contains(readResult.ContentText(), "Body") {
		t.Fatalf("expected cached attachment read, got %s", readResult.ContentText())
	}
}

func TestFilePreviewResolvesAttachmentMaterialID(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "post-1", "report.html")
	writeTestFile(t, filePath, "<h1>Material Preview</h1>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agentcontract.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "report.html",
				ContentType: "text/html",
				Path:        "inbox/mattermost/post-1/report.html",
			},
		},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_preview",
		Input:    toolcontract.MarshalToolInput(map[string]any{"materialID": "mattermost:file-1"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected material file_preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "Material Preview") {
		t.Fatalf("expected material preview content, got %s", previewResult.ContentText())
	}
}

func TestFilePreviewFallsBackFromStaleAttachmentPathToMaterialID(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "post-1", "report.html")
	writeTestFile(t, filePath, "<h1>Recovered Preview</h1>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		VisibleContext: agentcontract.VisibleContext{
			CurrentMaterials: []agentcontract.VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Filename:    "report.html",
				ContentType: "text/html",
				Path:        "inbox/mattermost/old/report.html",
			}},
		},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agentcontract.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "report.html",
				ContentType: "text/html",
				Path:        "inbox/mattermost/post-1/report.html",
			},
		},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_preview",
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": "inbox/mattermost/thread-1/post-1/report.html"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected stale path fallback success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "Recovered Preview") {
		t.Fatalf("expected recovered preview content, got %s", previewResult.ContentText())
	}
}

func TestFileReadFallsBackFromStaleAttachmentPathToMaterialID(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "post-1", "kim-intern-automation.html")
	writeTestFile(t, filePath, "<h1>Recovered Read</h1>\n<p>Body</p>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		VisibleContext: agentcontract.VisibleContext{
			CurrentMaterials: []agentcontract.VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Filename:    "kim-intern-automation.html",
				ContentType: "text/html",
				Path:        "inbox/mattermost/old/kim-intern-automation.html",
			}},
		},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agentcontract.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "kim-intern-automation.html",
				ContentType: "text/html",
				Path:        "inbox/mattermost/post-1/kim-intern-automation.html",
			},
		},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": "inbox/mattermost/thread-1/post-1/kim-intern-automation.html"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() {
		t.Fatalf("expected stale path file_read fallback success, got %s", readResult.ContentText())
	}
	if !strings.Contains(readResult.ContentText(), "Recovered Read") || strings.Contains(readResult.ContentText(), `"source":"attachmentPreview"`) {
		t.Fatalf("expected exact recovered file read, got %s", readResult.ContentText())
	}
}

func TestFileReadRejectsImageAttachmentMaterialFallback(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		VisibleContext: agentcontract.VisibleContext{
			CurrentMaterials: []agentcontract.VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Filename:    "mascot.png",
				ContentType: "image/png",
				Path:        "home/inbox/mattermost/old/mascot.png",
			}},
		},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agentcontract.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "mascot.png",
				ContentType: "image/png",
				Path:        "home/inbox/mattermost/post-1/mascot.png",
			},
		},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": "home/inbox/mattermost/thread-1/post-1/mascot.png"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !readResult.Failed() || !strings.Contains(readResult.ContentText(), "use image_read") {
		t.Fatalf("expected image attachment file_read fallback to point at image_read, got %s", readResult.ContentText())
	}
}

func TestFilePreviewUsesResolvedAttachmentPreviewWithoutWorkspaceStat(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agentcontract.VisibleContextMaterial{
				MaterialID:        "mattermost:file-1",
				Filename:          "report.html",
				ContentType:       "text/html",
				Path:              "home/inbox/mattermost/post-1/report.html",
				MarkdownPreview:   "<h1>Resolved Preview</h1>",
				ConversionStatus:  "converted",
				ConversionMessage: "raw text preview",
			},
		},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_preview",
		Input:    toolcontract.MarshalToolInput(map[string]any{"materialID": "mattermost:file-1"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected resolved attachment preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "Resolved Preview") {
		t.Fatalf("expected resolved preview content, got %s", previewResult.ContentText())
	}
}

func TestFileEditReplacesSingleExactMatch(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "source.ts")
	writeTestFile(t, filePath, "const title = 'Old';\n")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	editResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_edit",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"edits": []map[string]string{
				{"path": "tmp/source.ts", "oldText": "Old", "newText": "New"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if editResult.Failed() {
		t.Fatalf("expected file_edit success, got %s", editResult.ContentText())
	}
	document, errorValue := os.ReadFile(filePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != "const title = 'New';\n" {
		t.Fatalf("expected exact replacement, got %q", string(document))
	}
	expectedEditedPath := "tmp/source.ts"
	assertFileResourceEffect(t, editResult, "file", "updated", expectedEditedPath)
	assertFileResourceEffect(t, editResult, "workspace", "modified", expectedEditedPath)
}

func TestFileEditRejectsAmbiguousExactMatch(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "source.ts")
	writeTestFile(t, filePath, "same\nsame\n")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	editResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_edit",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"edits": []map[string]string{
				{"path": "tmp/source.ts", "oldText": "same", "newText": "changed"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !editResult.Failed() {
		t.Fatalf("expected ambiguous file_edit to fail, got %s", editResult.ContentText())
	}
	if editResult.Failure.Stage != "file_edit" || !strings.Contains(editResult.ContentText(), `"matchCount":2`) {
		t.Fatalf("expected match count failure, got %+v %s", editResult.Failure, editResult.ContentText())
	}
	document, errorValue := os.ReadFile(filePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != "same\nsame\n" {
		t.Fatalf("expected failed edit not to modify file, got %q", string(document))
	}
}

func TestFilePatchAppliesMultipleExactEdits(t *testing.T) {
	workspacePath := t.TempDir()
	firstPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "one.ts")
	secondPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "two.ts")
	writeTestFile(t, firstPath, "alpha")
	writeTestFile(t, secondPath, "beta")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	patchResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_edit",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"edits": []map[string]string{
				{"path": "tmp/one.ts", "oldText": "alpha", "newText": "ALPHA"},
				{"path": "tmp/two.ts", "oldText": "beta", "newText": "BETA"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if patchResult.Failed() {
		t.Fatalf("expected file_edit success, got %s", patchResult.ContentText())
	}
	assertTestFileContent(t, firstPath, "ALPHA")
	assertTestFileContent(t, secondPath, "BETA")
}

func TestFilePatchValidationIsAllOrNothing(t *testing.T) {
	workspacePath := t.TempDir()
	firstPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "one.ts")
	secondPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "two.ts")
	writeTestFile(t, firstPath, "alpha")
	writeTestFile(t, secondPath, "beta")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	patchResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_edit",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"edits": []map[string]string{
				{"path": "tmp/one.ts", "oldText": "alpha", "newText": "ALPHA"},
				{"path": "tmp/two.ts", "oldText": "missing", "newText": "BETA"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !patchResult.Failed() {
		t.Fatalf("expected file_edit failure, got %s", patchResult.ContentText())
	}
	if !strings.Contains(patchResult.ContentText(), `"editIndex":1`) || !strings.Contains(patchResult.ContentText(), `"matchCount":0`) {
		t.Fatalf("expected failing edit metadata, got %s", patchResult.ContentText())
	}
	assertTestFileContent(t, firstPath, "alpha")
	assertTestFileContent(t, secondPath, "beta")
}

func withoutDirectoryAccess(t *testing.T, directoryPath string) {
	t.Helper()
	if errorValue := os.Chmod(directoryPath, 0000); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() {
		_ = os.Chmod(directoryPath, 0700)
	})
}

func TestFileWriteFailsWithAccessDeniedWhenPOSIXDeniesCircleDirectory(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.md"), "secret")
	withoutDirectoryAccess(t, financeDirectoryPath)
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    filepath.Join(financeDirectoryPath, "report.md"),
			"content": "changed",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !writeResult.Failed() || writeResult.FailureCode() != toolcontract.FailureCodes.AccessDenied.String() {
		t.Fatalf("expected the OS write denial to surface as access_denied, got %+v", writeResult)
	}
}

func TestFileDeliverFailsWithAccessDeniedWhenPOSIXDeniesCircleDirectory(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.md"), "secret")
	withoutDirectoryAccess(t, financeDirectoryPath)
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	attachResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": filepath.Join(financeDirectoryPath, "report.md"),
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !attachResult.Failed() || attachResult.FailureCode() != toolcontract.FailureCodes.AccessDenied.String() {
		t.Fatalf("expected the OS read denial to surface as access_denied, got %+v", attachResult)
	}
}

func TestFileReadFailsWithAccessDeniedWhenPOSIXDeniesCircleDirectory(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.pdf"), "secret")
	withoutDirectoryAccess(t, financeDirectoryPath)
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": filepath.Join(financeDirectoryPath, "report.pdf"),
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.AccessDenied.String() {
		t.Fatalf("expected the OS read denial to surface as access_denied, got %+v", result)
	}
}

func TestFileReadAllowsCirclePathWhenPOSIXAllows(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.md"), "secret")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": filepath.Join(financeDirectoryPath, "report.md"),
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || !strings.Contains(result.ContentText(), `"content":"secret"`) {
		t.Fatalf("expected file_read success, got %+v", result)
	}
}

func TestFileWriteAllowsCirclePathWhenPOSIXAllows(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    filepath.Join(workspacePath, "circles", "finance", "report.md"),
			"content": "finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected finance member write success, got %+v", result)
	}
	assertTestFileContent(t, filepath.Join(workspacePath, "circles", "finance", "report.md"), "finance")
}

func TestFileWriteDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "notes.md",
			"content": "private",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected private write success, got %+v", result)
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "notes.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "private" {
		t.Fatalf("expected private file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
	if !strings.Contains(result.ContentText(), `"notes.md"`) {
		t.Fatalf("expected private agent path in result, got %s", result.ContentText())
	}
}

func TestFileWriteDefaultsToCircleScopeForCircleChannel(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		ConversationID:          "channel:channel-1",
		ConversationType:        "P",
		ConversationChannelID:   "channel-1",
		ConversationChannelName: "circle-finance",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "report.md",
			"content": "finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected circle write success, got %+v", result)
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "report.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "finance" {
		t.Fatalf("expected finance file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
}

func TestFileWriteDefaultsToStaffScopeForGeneralChannel(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		ConversationID:          "thread:channel-1:post-1",
		ConversationType:        "O",
		ConversationChannelID:   "channel-1",
		ConversationChannelName: "town-square",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "status.md",
			"content": "staff",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected staff write success, got %+v", result)
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "status.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "staff" {
		t.Fatalf("expected staff file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
}

func TestFileAttachDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	privateDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1")
	if errorValue := os.MkdirAll(privateDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(privateDirectoryPath, "notes.md"), "private")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": "notes.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected private delivery success, got %+v", result)
	}
	if result.Attachments[0].DevicePath != "/workspace/private/people/person-1/notes.md" {
		t.Fatalf("expected private device path, got %+v", result.Attachments[0])
	}
}

func TestFileDeliverPersistsDocumentToDocuments(t *testing.T) {
	workspacePath := t.TempDir()
	draftDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	if errorValue := os.MkdirAll(draftDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(draftDirectoryPath, "report.docx"), "docx-bytes")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input:    toolcontract.MarshalToolInput(map[string]string{"path": "tmp/deck/report.docx"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected delivery success, got %+v", result)
	}
	persistedPath := filepath.Join(workspacePath, "private", "people", "person-1", "documents", "report.docx")
	if _, errorValue := os.Stat(persistedPath); errorValue != nil {
		t.Fatalf("expected delivered .docx auto-persisted to ~/documents, stat failed: %v", errorValue)
	}
}

func TestFileDeliverCanDeliverDraftOutput(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/build/deck.pptx",
			"content": "pptx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file_write success, got %s", writeResult.ContentText())
	}
	deliverResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input: toolcontract.MarshalToolInput(map[string]any{
			"path": "tmp/deck/build/deck.pptx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if deliverResult.Failed() {
		t.Fatalf("expected file_deliver success, got %s", deliverResult.ContentText())
	}
	if len(deliverResult.Attachments) != 1 {
		t.Fatalf("expected one delivered file, got %+v", deliverResult.Attachments)
	}
	expectedDevicePath := "/workspace/private/people/person-1/tmp/deck/build/deck.pptx"
	if deliverResult.Attachments[0].DevicePath != expectedDevicePath {
		t.Fatalf("expected delivered draft path, got %+v", deliverResult.Attachments[0])
	}
}

func TestFileDeliverResolvesSamePathSpellingsAsTerminalWrite(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	runResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command": "mkdir -p ~/documents && printf docx-bytes > ~/documents/report.docx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runResult.Failed() {
		t.Fatalf("expected shell success, got %s", runResult.ContentText())
	}

	expectedDevicePath := "/workspace/private/people/person-1/documents/report.docx"
	for _, path := range []string{
		"~/documents/report.docx",
		"documents/report.docx",
		filepath.Join(workspacePath, "private", "people", "person-1", "documents", "report.docx"),
	} {
		deliverResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
			ToolName: toolcontract.FileDeliverToolName,
			Input:    toolcontract.MarshalToolInput(map[string]string{"path": path}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if deliverResult.Failed() {
			t.Fatalf("expected file_deliver success for path spelling %q, got %s", path, deliverResult.ContentText())
		}
		if len(deliverResult.Attachments) != 1 || deliverResult.Attachments[0].DevicePath != expectedDevicePath {
			t.Fatalf("expected delivery of the terminal-written file for path spelling %q, got %+v", path, deliverResult.Attachments)
		}
	}
}

func TestFileDeliverNotFoundIncludesCandidateFiles(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	runResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command": "mkdir -p ~/documents && printf docx-bytes > ~/documents/'Han River Ops 2026 Q2 Operations Review.docx'",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runResult.Failed() {
		t.Fatalf("expected shell success, got %s", runResult.ContentText())
	}

	deliverResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input:    toolcontract.MarshalToolInput(map[string]string{"path": "~/documents/Q2 Operations Review.docx"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !deliverResult.Failed() || deliverResult.Failure.Code != "not_found" {
		t.Fatalf("expected not_found failure, got %+v", deliverResult)
	}
	var failureData struct {
		CandidateFiles []string `json:"candidateFiles"`
	}
	if errorValue := json.Unmarshal(deliverResult.Output.Data, &failureData); errorValue != nil {
		t.Fatalf("expected structured failure data, got error %v for %s", errorValue, string(deliverResult.Output.Data))
	}
	expectedCandidatePath := "~/documents/Han River Ops 2026 Q2 Operations Review.docx"
	found := false
	for _, candidatePath := range failureData.CandidateFiles {
		if candidatePath == expectedCandidatePath {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected candidateFiles to include %q, got %+v", expectedCandidatePath, failureData.CandidateFiles)
	}
}

func TestFileWriteAllowsManagedSitePackageManifest(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"file_write"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	managedResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "~/sites/site-1/draft/app/package.json",
			"content": `{"project":"user-owned package manifest"}`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if managedResult.Failed() {
		t.Fatalf("expected the user-owned site manifest to be writable; build gates own the invariant, got %+v", managedResult)
	}

	tmpResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "tmp/demo/package.json",
			"content": `{"project":"freeform package file"}`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if tmpResult.Failed() {
		t.Fatalf("expected tmp package manifest write to remain allowed, got %+v", tmpResult)
	}
}

func TestFileWriteThroughWorkspaceActorTreatsContentAsData(t *testing.T) {
	workspacePath := t.TempDir()
	previousMask := syscall.Umask(0077)
	defer syscall.Umask(previousMask)
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	content := "hello\n$(touch should-not-exist)\n"
	writeResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"path":    "tmp/deck/input.txt",
			"content": content,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file_write success, got %s", writeResult.ContentText())
	}

	taskTmpPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp")
	document, errorValue := os.ReadFile(filepath.Join(taskTmpPath, "deck", "input.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != content {
		t.Fatalf("expected exact content, got %q", string(document))
	}
	if _, errorValue := os.Stat(filepath.Join(taskTmpPath, "should-not-exist")); !os.IsNotExist(errorValue) {
		t.Fatalf("file_write content must not be executed as shell, stat error %v", errorValue)
	}
	fileInformation, errorValue := os.Stat(filepath.Join(taskTmpPath, "deck", "input.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if fileInformation.Mode().Perm() != 0600 {
		t.Fatalf("expected umask 077 to yield mode 0600, got %v", fileInformation.Mode().Perm())
	}
}

func TestFileWriteRespectsRequesterUmaskLikeTerminalRun(t *testing.T) {
	workspacePath := t.TempDir()
	previousMask := syscall.Umask(0027)
	defer syscall.Umask(previousMask)

	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "ok",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file_write success, got %s", writeResult.ContentText())
	}

	deckDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	directoryInformation, errorValue := os.Stat(deckDirectoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if directoryInformation.Mode().Perm() != 0750 {
		t.Fatalf("expected umask 027 to yield directory mode 0750 exactly as shell would, got mode %v", directoryInformation.Mode().Perm())
	}
	fileInformation, errorValue := os.Stat(filepath.Join(deckDirectoryPath, "input.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if fileInformation.Mode().Perm() != 0640 {
		t.Fatalf("expected umask 027 to yield file mode 0640 exactly as shell would, got mode %v", fileInformation.Mode().Perm())
	}
}

func TestFileWriteAndTerminalRunShareRequesterWorkspaceActorView(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "same workspace",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file_write success, got %s", writeResult.ContentText())
	}

	runResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"workingDirectoryPath": "tmp/deck",
			"command":              "mkdir -p build && cat input.txt > build/output.txt",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runResult.Failed() {
		t.Fatalf("expected shell success, got %s", runResult.ContentText())
	}
	outputPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck", "build", "output.txt")
	document, errorValue := os.ReadFile(outputPath)
	if errorValue != nil || string(document) != "same workspace" {
		t.Fatalf("expected terminal to read file_write output, got %q and %v", string(document), errorValue)
	}
}

func TestFileWriteRejectsLegacyMode(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})
	toolContext := toolcontract.WithTaskRunID(context.Background(), "run-mode-regression")

	writeResult, errorValue := toolRegistry.Invoke(toolContext, toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"path":    "tmp/docx-guide/document.json",
			"content": `{"title":"readable"}`,
			"mode":    644,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !writeResult.Failed() || writeResult.FailureStage() != "tool_input_schema" {
		t.Fatalf("expected legacy mode to be rejected, got %+v", writeResult)
	}
}

func TestFileWriteWithoutRequesterIdentityDoesNotFallbackToServiceUser(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "no service fallback",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure.Stage != "actor_identity_missing" {
		t.Fatalf("expected actor identity failure, got %+v", result)
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "tmp", "deck", "input.txt")); !os.IsNotExist(errorValue) {
		t.Fatalf("file_write must not fall back to service-user workspace writes, stat error %v", errorValue)
	}
}

func TestFileWriteRejectsBuiltInSkillPaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, path := range []string{
		"/workspace/skills/bash/SKILL.md",
		"/workspace/skills/.internkim-skills-manifest.json",
		"/workspace/.agents/skills/agent-browser/SKILL.md",
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
			ToolName: "file_write",
			Input: toolcontract.MarshalToolInput(map[string]string{
				"path":    path,
				"content": "no",
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() {
			t.Fatalf("expected file_write to reject immutable skill path %q", path)
		}
	}
}

func TestApplyExactOrTolerantEditExactMatch(t *testing.T) {
	updated, count, applied := applyExactOrTolerantEdit("alpha\nbeta\ngamma\n", "beta", "BETA")
	if !applied || count != 1 || updated != "alpha\nBETA\ngamma\n" {
		t.Fatalf("expected exact replace, got applied=%v count=%d %q", applied, count, updated)
	}
}

func TestApplyExactOrTolerantEditRejectsAmbiguous(t *testing.T) {
	_, count, applied := applyExactOrTolerantEdit("same\nsame\n", "same", "changed")
	if applied || count != 2 {
		t.Fatalf("expected ambiguous rejection with count 2, got applied=%v count=%d", applied, count)
	}
}

func TestApplyExactOrTolerantEditToleratesLeadingWhitespace(t *testing.T) {
	content := "func main() {\n        callDeep()\n}\n"
	// model wrote the block with 4-space indent; file has 8 spaces
	updated, _, applied := applyExactOrTolerantEdit(content, "    callDeep()", "    callShallow()")
	if !applied {
		t.Fatal("expected leading-whitespace-tolerant match to apply")
	}
	if updated != "func main() {\n        callShallow()\n}\n" {
		t.Fatalf("expected replacement re-indented to file's 8 spaces, got %q", updated)
	}
}

func TestApplyExactOrTolerantEditToleratesTrailingWhitespace(t *testing.T) {
	// File lines carry trailing whitespace the model omitted, so the exact
	// multi-line substring does not match and the tolerant tier must apply.
	content := "alpha  \nbeta\n"
	updated, _, applied := applyExactOrTolerantEdit(content, "alpha\nbeta", "ALPHA\nBETA")
	if !applied || updated != "ALPHA\nBETA\n" {
		t.Fatalf("expected trailing-whitespace-tolerant match, got applied=%v %q", applied, updated)
	}
}

func TestApplyExactOrTolerantEditNormalizesSmartQuotes(t *testing.T) {
	content := "const greeting = ‘hello’;\n"
	updated, _, applied := applyExactOrTolerantEdit(content, "const greeting = 'hello';", "const greeting = 'hi';")
	if !applied || updated != "const greeting = 'hi';\n" {
		t.Fatalf("expected smart-quote normalized match, got applied=%v %q", applied, updated)
	}
}

func TestApplyExactOrTolerantEditFailsWhenAbsent(t *testing.T) {
	_, count, applied := applyExactOrTolerantEdit("alpha\nbeta\n", "totally-different-line", "x")
	if applied || count != 0 {
		t.Fatalf("expected absent oldText to fail, got applied=%v count=%d", applied, count)
	}
}

func TestFileEditToleratesInlineWhitespaceDriftWithBytePreciseSpan(t *testing.T) {
	content := "const total = compute(a,   b)\t+ 1;\nconst other = keep(x, y);\n"
	updated, matchCount, applied := applyExactOrTolerantEdit(content, "compute(a, b) +", "sum(a, b) +")
	if !applied {
		t.Fatalf("expected inline-whitespace-tolerant match, applied=%v count=%d", applied, matchCount)
	}
	expected := "const total = sum(a, b) + 1;\nconst other = keep(x, y);\n"
	if updated != expected {
		t.Fatalf("expected byte-precise replacement, got %q", updated)
	}
}

func TestFileEditWhitespaceToleranceStillRequiresUniqueMatch(t *testing.T) {
	content := "call(a,  b)\ncall(a,   b)\n"
	_, matchCount, applied := applyExactOrTolerantEdit(content, "call(a, b)", "call(z)")
	if applied || matchCount < 2 {
		t.Fatalf("whitespace-equivalent duplicates must not apply, applied=%v count=%d", applied, matchCount)
	}
}

func TestFileEditMatchFailureGuidanceSuggestsClosestLines(t *testing.T) {
	guidance := fileEditMatchFailureGuidance("export const Button = () => null;\n", "export const Buttons = () => null;", 0)
	if !strings.Contains(guidance, "closest current lines") || !strings.Contains(guidance, "export const Button") {
		t.Fatalf("expected closest-line suggestion, got %q", guidance)
	}
	if strings.Contains(guidance, "file_write") {
		t.Fatalf("edit-failure guidance must not nudge a full rewrite, got %q", guidance)
	}
}

func TestFileEditMatchFailureGuidanceAnchorsOnDistinctiveLineNotDelimiters(t *testing.T) {
	content := "---\nversion: alpha\nname: \"Bridgeworks consulting guide\"\ncolors:\n  primary: \"#111111\"\n---\nbody text\n"
	oldText := "---\nversion: alpha\nname: \"Bridgeworks small business consulting guide\"\ncolors:\n"
	guidance := fileEditMatchFailureGuidance(content, oldText, 0)
	if !strings.Contains(guidance, "name: \"Bridgeworks consulting guide\"") || !strings.Contains(guidance, "colors:") {
		t.Fatalf("expected a copyable context window around the distinctive line, got %q", guidance)
	}
}
