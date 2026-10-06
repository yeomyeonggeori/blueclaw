//go:build !nobundledharness

package agentruntime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type taskFixture struct {
	workspacePath string
	builder       *ToolCatalogBuilder
	request       ToolCatalogRequest
	taskRun       agentcontract.TaskRun
	standIn       *recordCatalogStandIn
}

func newTaskFixture(t *testing.T, descriptors ...capability.ToolDescriptor) taskFixture {
	t.Helper()
	workspacePath := t.TempDir()
	terminalService := security.NewShellService(config.TerminalConfiguration{
		WorkspaceRootPath:     workspacePath,
		Mode:                  "native",
		TimeoutSecond:         5,
		OutputMaxBytes:        65536,
		SessionMaxCount:       2,
		AllowInteractiveShell: true,
	})
	builder, _ := aBuilderWith(descriptors...)
	builder.UseWorkspaceRootPath(workspacePath)
	builder.UseTerminalService(terminalService)
	builder.UseWorkspaceActorFactory(security.NewDirectWorkspaceActorFactory(terminalService))
	allowedToolNames := internalTestToolNames()
	for _, descriptor := range descriptors {
		allowedToolNames = append(allowedToolNames, descriptor.Name)
	}
	builder.UseAllowedToolNamesByProfile(nil, allowedToolNames)
	builder.UseCompanyProvider(func() agentcontract.CompanyContext { return agentcontract.CompanyContext{TimeZone: "Asia/Seoul"} })
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	builder.UseTaskRunService(taskRunService)
	standIn := &recordCatalogStandIn{discovered: descriptors}
	request := aRequestFrom(standIn, "sample@example.com")
	request.ProfileName = "default"
	request.RequesterPersonID = "person-1"
	request.RequesterName = "이샘플"
	request.ConversationID = "dm:channel-1"
	request.PersonAccess = policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}}
	return taskFixture{
		workspacePath: workspacePath,
		builder:       builder,
		request:       request,
		taskRun:       taskRunService.CreateTaskRun("person-1", "dm:channel-1", "견적서 만들어 줘"),
		standIn:       standIn,
	}
}

func (fixture taskFixture) homePath() string {
	return filepath.Join(fixture.workspacePath, "private", "people", "person-1")
}

func (fixture taskFixture) taskContext() context.Context {
	return toolcontract.WithTaskRunID(context.Background(), fixture.taskRun.TaskRunID)
}

func (fixture taskFixture) invoke(t *testing.T, toolName string, input any) toolcontract.ToolResult {
	t.Helper()
	toolSet := fixture.builder.BuildToolSet(fixture.request)
	result, errorValue := toolSet.InvokeInternal(fixture.taskContext(), toolcontract.ToolInvocation{ToolName: toolName, Input: toolcontract.MarshalToolInput(input)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return result
}

func (fixture taskFixture) shellOutput(t *testing.T, command string) string {
	t.Helper()
	result := fixture.invoke(t, "bash", map[string]any{"command": command})
	if result.Failed() {
		t.Fatalf("the command %q failed: %s", command, result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	return commandResult.Stdout
}
