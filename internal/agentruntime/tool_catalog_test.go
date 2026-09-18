package agentruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type memoryScheduleRepository struct {
	schedules []task.Schedule
	failed    []string
}

func TestResolveAgentWorkspaceReferencesUsesPathBoundaries(t *testing.T) {
	workspaceRootPath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspaceRootPath)
	testCases := []struct {
		input    string
		expected string
	}{
		{
			input:    "python3 /workspace/skills/document/scripts/export.py",
			expected: "python3 " + workspaceRootPath + "/skills/document/scripts/export.py",
		},
		{
			input:    "cd '/workspace' && pwd",
			expected: "cd '" + workspaceRootPath + "' && pwd",
		},
		{
			input:    "WORKSPACE=/workspace",
			expected: "WORKSPACE=" + workspaceRootPath,
		},
		{
			input:    "cat " + workspaceRootPath + "/documents/report.md",
			expected: "cat " + workspaceRootPath + "/documents/report.md",
		},
		{
			input:    "cat /other/workspace/documents/report.md",
			expected: "cat /other/workspace/documents/report.md",
		},
		{
			input:    "echo /workspaceBackup",
			expected: "echo /workspaceBackup",
		},
	}
	for _, testCase := range testCases {
		actual := toolCatalogBuilder.resolveAgentWorkspaceReferences(testCase.input)
		if actual != testCase.expected {
			t.Fatalf("expected %q, got %q", testCase.expected, actual)
		}
	}
}

func TestResolveAgentWorkspaceEnvironmentLeavesConcretePathsUnchanged(t *testing.T) {
	workspaceRootPath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspaceRootPath)

	resolved := toolCatalogBuilder.resolveAgentWorkspaceEnvironment(map[string]string{
		"HOME":     workspaceRootPath + "/private/people/person-1",
		"TOOL_DIR": "/workspace/tools",
	})

	if resolved["HOME"] != workspaceRootPath+"/private/people/person-1" ||
		resolved["TOOL_DIR"] != workspaceRootPath+"/tools" {
		t.Fatalf("expected exact concrete environment paths, got %+v", resolved)
	}
}

func (repository *memoryScheduleRepository) UpsertSchedule(schedule task.Schedule) error {
	repository.schedules = append(repository.schedules, schedule)
	return nil
}

func (repository *memoryScheduleRepository) UpdateSchedule(request task.ScheduleUpdateRequest) (task.ScheduleUpdateResult, error) {
	for index, schedule := range repository.schedules {
		if !memoryScheduleMatchesUpdateRequest(schedule, request) {
			continue
		}
		updatedSchedule := schedule
		var errorValue error
		if request.UpdateSchedule != nil {
			updatedSchedule, errorValue = request.UpdateSchedule(schedule)
			if errorValue != nil {
				return task.ScheduleUpdateResult{}, errorValue
			}
		}
		repository.schedules[index] = updatedSchedule
		return task.ScheduleUpdateResult{Schedule: updatedSchedule, IsFound: true}, nil
	}
	return task.ScheduleUpdateResult{}, nil
}

func (repository *memoryScheduleRepository) ClaimDueSchedules(int, time.Duration, time.Time, string) ([]task.Schedule, error) {
	return append([]task.Schedule{}, repository.schedules...), nil
}

func (repository *memoryScheduleRepository) MarkScheduleSucceeded(schedule task.Schedule) error {
	repository.schedules = []task.Schedule{schedule}
	return nil
}

func (repository *memoryScheduleRepository) MarkScheduleFailed(_ task.Schedule, errorMessage string, _ time.Time) error {
	repository.failed = append(repository.failed, errorMessage)
	return nil
}

func (repository *memoryScheduleRepository) ExpireSchedule(schedule task.Schedule, errorMessage string, referenceTime time.Time) error {
	schedule.ExpiresAt = timePointer(referenceTime)
	schedule.NextRunAt = nil
	schedule.LastError = errorMessage
	repository.schedules = append(repository.schedules, schedule)
	return nil
}

func (repository *memoryScheduleRepository) CancelSchedules(request task.ScheduleCancelRequest) (task.ScheduleCancelResult, error) {
	cancelledSchedules := []task.Schedule{}
	remainingSchedules := []task.Schedule{}
	for _, schedule := range repository.schedules {
		if memoryScheduleMatchesCancelRequest(schedule, request) {
			schedule.ExpiresAt = timePointer(request.CancelledAt)
			schedule.NextRunAt = nil
			cancelledSchedules = append(cancelledSchedules, schedule)
			continue
		}
		remainingSchedules = append(remainingSchedules, schedule)
	}
	repository.schedules = append(remainingSchedules, cancelledSchedules...)
	return task.ScheduleCancelResult{Schedules: cancelledSchedules}, nil
}

func (repository *memoryScheduleRepository) ListSchedules(request task.ScheduleListRequest) (task.ScheduleListResult, error) {
	schedules := []task.Schedule{}
	for _, schedule := range repository.schedules {
		if !memoryScheduleMatchesListRequest(schedule, request) {
			continue
		}
		schedules = append(schedules, schedule)
	}
	pageSize := request.PageSize
	if pageSize <= 0 || pageSize > len(schedules) {
		pageSize = len(schedules)
	}
	return task.ScheduleListResult{
		Schedules:  append([]task.Schedule{}, schedules[:pageSize]...),
		TotalCount: len(schedules),
		Page:       1,
		PageSize:   pageSize,
	}, nil
}

func memoryScheduleMatchesUpdateRequest(schedule task.Schedule, request task.ScheduleUpdateRequest) bool {
	if schedule.ScheduleID != request.ScheduleID {
		return false
	}
	if schedule.CreatorPersonID != request.RequesterPersonID {
		return false
	}
	return schedule.NextRunAt != nil
}

func memoryScheduleMatchesCancelRequest(schedule task.Schedule, request task.ScheduleCancelRequest) bool {
	if schedule.NextRunAt == nil {
		return false
	}
	switch request.Scope {
	case task.ScheduleCancelScopeCurrentConversation:
		return schedule.ConversationID == request.ConversationID
	case task.ScheduleCancelScopeScheduleIDs:
		if !containsString(request.ScheduleIDs, schedule.ScheduleID) {
			return false
		}
		return schedule.CreatorPersonID == request.RequesterPersonID || schedule.ConversationID == request.ConversationID
	default:
		return schedule.CreatorPersonID == request.RequesterPersonID
	}
}

func memoryScheduleMatchesListRequest(schedule task.Schedule, request task.ScheduleListRequest) bool {
	if request.CreatorPersonID != "" && schedule.CreatorPersonID != request.CreatorPersonID {
		return false
	}
	if request.ConversationID != "" && schedule.ConversationID != request.ConversationID {
		return false
	}
	return request.IncludeExpired || schedule.NextRunAt != nil
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func terminalTestCanResolveBun() bool {
	for _, path := range strings.Split(security.CanonicalRuntimePATH, ":") {
		path = filepath.Join(path, "bun")
		if information, errorValue := os.Stat(path); errorValue == nil && !information.IsDir() {
			return true
		}
	}
	return false
}

func TestRequesterWorkspaceToolHandlersDoNotUseDirectFileMutation(t *testing.T) {
	document, errorValue := os.ReadFile("tool_catalog.go")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	source := string(document)
	for _, forbidden := range []string{
		"os.WriteFile(",
		"os.ReadFile(",
		"os.OpenFile(",
		"copyRegularFile(",
		"verifyRequesterTerminalCanReadFile(",
		"writeFileAsRequester(",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("requester workspace tool handlers must use WorkspaceActor, found %s", forbidden)
		}
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if errorValue := os.MkdirAll(filepath.Dir(path), 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(path, []byte(content), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func assertTestFileContent(t *testing.T, path string, expectedContent string) {
	t.Helper()
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(content) != expectedContent {
		t.Fatalf("expected %q, got %q", expectedContent, string(content))
	}
}

func newFileToolTestCatalogBuilder(workspacePath string) *ToolCatalogBuilder {
	terminalService := security.NewShellService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "virtualMachineGuest",
		TimeoutSecond:     30,
	})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseWorkspaceActorFactory(security.NewDirectWorkspaceActorFactory(terminalService))
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, internalTestToolNames())
	return toolCatalogBuilder
}

func newTerminalToolTestCatalogBuilder(workspacePath string) *ToolCatalogBuilder {
	terminalService := security.NewShellService(config.TerminalConfiguration{
		WorkspaceRootPath:     workspacePath,
		Mode:                  "virtualMachineGuest",
		TimeoutSecond:         5,
		OutputMaxBytes:        4096,
		SessionMaxCount:       2,
		AllowInteractiveShell: true,
	})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseWorkspaceActorFactory(security.NewDirectWorkspaceActorFactory(terminalService))
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, internalTestToolNames())
	return toolCatalogBuilder
}

func internalTestToolNames() []string {
	return []string{
		"conversation_history",
		"read",
		"document_read",
		"file_deliver",
		"file_edit",
		"file_preview",
		"file_read",
		"file_write",
		"image_read",
		"skill_add",
		"skill_remove",
		"skill_search",
		"shell",
	}
}

func findToolDefinition(toolDefinitions []toolcontract.ToolDefinition, toolName string) (toolcontract.ToolDefinition, bool) {
	for _, toolDefinition := range toolDefinitions {
		if toolDefinition.Name == toolName {
			return toolDefinition, true
		}
	}
	return toolcontract.ToolDefinition{}, false
}

func siteSourceBundlePaths(t *testing.T, bundleBase64 string) []string {
	t.Helper()
	document, errorValue := base64.StdEncoding.DecodeString(bundleBase64)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	gzipReader, errorValue := gzip.NewReader(bytes.NewReader(document))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	paths := []string{}
	for {
		header, errorValue := tarReader.Next()
		if errorValue == io.EOF {
			return paths
		}
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		paths = append(paths, header.Name)
	}
}

func userSkillDocument(skillName string) string {
	return `---
name: ` + skillName + `
description: Research source material and organize source lookups when the user asks for research help.
tool-references: memory_search
---
Research helper handles source lookups.
`
}

func decodeSkillAddResult(t *testing.T, content string) skillAddResult {
	t.Helper()
	var resultDocument skillAddResult
	if errorValue := json.Unmarshal([]byte(content), &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	return resultDocument
}

func containsTestString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type staticAttachmentMaterialResolver struct {
	material agentcontract.VisibleContextMaterial
}

func (resolver staticAttachmentMaterialResolver) ResolveAttachmentMaterial(context.Context, string) (agentcontract.VisibleContextMaterial, error) {
	return resolver.material, nil
}

type skillSearchTestRetriever struct{}

func (skillSearchTestRetriever) Available(_ agentcontract.AgentRequest, skillInstructions []agentcontract.SkillInstruction) []agentcontract.SkillInstruction {
	return skillInstructions
}

func (skillSearchTestRetriever) Retrieve(context.Context, agentcontract.AgentRequest, []agentcontract.SkillInstruction, int) agentcontract.SkillRetrievalResult {
	return agentcontract.SkillRetrievalResult{}
}

func (skillSearchTestRetriever) Search(_ context.Context, _ agentcontract.AgentRequest, _ []agentcontract.SkillInstruction, querySet agentcontract.SkillSearchQuerySet, _ int) agentcontract.SkillRetrievalResult {
	if len(querySet.Queries) == 0 {
		return agentcontract.SkillRetrievalResult{}
	}
	return agentcontract.SkillRetrievalResult{
		RetrievalMode: "embedding",
		IndexStatus:   "ready",
		SelectedCandidates: []agentcontract.SkillCandidate{{
			Name:   "mail",
			Score:  0.91,
			Reason: "embedding_similarity",
		}},
	}
}

func (skillSearchTestRetriever) Refresh(context.Context, []agentcontract.SkillInstruction) {}
