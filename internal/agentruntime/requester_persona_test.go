package agentruntime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type personaActorFactory struct {
	documents map[string][]byte
	readPaths []string
	requested []string
}

type personaActor struct {
	factory *personaActorFactory
}

func (factory *personaActorFactory) CanListDirectory(context.Context) bool { return true }

func (factory *personaActorFactory) Requester(_ context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	factory.requested = append(factory.requested, request.PersonAccess.PersonID)
	return personaActor{factory: factory}, nil
}

func (actor personaActor) Run(context.Context, security.CommandRequest) (security.CommandResult, error) {
	return security.CommandResult{}, nil
}

func (actor personaActor) MkdirAll(context.Context, string) error { return nil }

func (actor personaActor) WriteFile(context.Context, string, []byte) error { return nil }

func (actor personaActor) ReadFile(_ context.Context, path string, _ int64) ([]byte, error) {
	actor.factory.readPaths = append(actor.factory.readPaths, path)
	document, isPresent := actor.factory.documents[path]
	if !isPresent {
		return nil, security.WorkspaceActorError{Operation: "read", Code: security.ActorErrorCodeNotFound, Detail: "not found"}
	}
	return document, nil
}

func (actor personaActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return nil, nil
}

func (actor personaActor) Stat(context.Context, string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{}, nil
}

func TestTheRequestersPersonaIsReadFromTheirOwnHomeAsThatPerson(t *testing.T) {
	workspacePath := t.TempDir()
	documentPath := filepath.Join(security.PersonHomeDirectoryPath(workspacePath, "person-1"), ".internkim", "user.json")
	factory := &personaActorFactory{documents: map[string][]byte{
		documentPath: []byte(`{"schemaVersion": 1, "callMe": "샘플님", "language": {"default": "ja-JP"}}`),
	}}

	user, isAvailable := readRequesterPersona(factory, policy.PersonAccess{PersonID: "person-1"}, workspacePath)

	if !isAvailable || user.CallMe != "샘플님" || user.Language == nil || user.Language.Default != "ja-JP" {
		t.Fatalf("expected the requester's document, got %+v", user)
	}
	if len(factory.requested) != 1 || factory.requested[0] != "person-1" {
		t.Fatalf("expected the read to run as the requester, got %v", factory.requested)
	}
	if _, isAvailable := readRequesterPersona(factory, policy.PersonAccess{PersonID: "person-2"}, workspacePath); isAvailable {
		t.Fatal("expected nothing for a person without a document")
	}
	if _, isAvailable := readRequesterPersona(nil, policy.PersonAccess{PersonID: "person-1"}, workspacePath); isAvailable {
		t.Fatal("expected nothing without an actor factory")
	}
}

func TestARequesterPersonaTheSchemaRefusesIsLeftOut(t *testing.T) {
	workspacePath := t.TempDir()
	documentPath := filepath.Join(security.PersonHomeDirectoryPath(workspacePath, "person-1"), ".internkim", "user.json")
	factory := &personaActorFactory{documents: map[string][]byte{
		documentPath: []byte(`{"schemaVersion": 1, "callMe": "샘플님", "nickname": "kim"}`),
	}}

	if user, isAvailable := readRequesterPersona(factory, policy.PersonAccess{PersonID: "person-1"}, workspacePath); isAvailable {
		t.Fatalf("expected a refused document to be left out, got %+v", user)
	}
}

func TestAHeldCallIsWordedInTheAgentsVoiceTowardThisRequester(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	factory := &personaActorFactory{documents: map[string][]byte{
		filepath.Join(security.PersonHomeDirectoryPath(workspacePath, "person-1"), ".internkim", "user.json"): []byte(`{"schemaVersion": 1, "callMe": "샘플님", "about": "결제 서버 담당", "tone": {"register": "formal"}}`),
	}}
	toolCatalogBuilder.UseWorkspaceActorFactory(factory)
	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseAgentPersonaProvider(func() persona.Agent {
		return persona.Agent{Identity: persona.Identity{Names: []string{"샘플봇"}}}
	})
	questionWorder := &recordingQuestionWorder{}
	gate := approvalgate.New(taskRunService)
	gate.UseQuestionWorder(questionWorder)
	gate.UsePermissionAsker(interruptedPermissionAsker{})
	taskLauncher.UseApprovalGate(gate)

	if _, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		RequesterPersonID:         "person-1",
		ConversationID:            "channel-1",
		Prompt:                    "Delete the draft.",
		PersonAccess:              policy.PersonAccess{PersonID: "person-1"},
		AccessibleConversationIDs: []string{"channel-1"},
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	holdACallOn(t, harness.LastTurnRequest().ToolSet, taskRunService.CreateTaskRun("person-1", "channel-1", "Delete the draft.").TaskRunID)

	if questionWorder.facts.Voice != "- Your name is 샘플봇.\n- Call them 샘플님.\n- Tone with them: keep a formal register." {
		t.Fatalf("expected the question to be worded in the agent's voice toward this requester, got %q", questionWorder.facts.Voice)
	}
	if len(factory.requested) != 1 {
		t.Fatalf("expected the requester's persona to be read once for the launch, got %d reads", len(factory.requested))
	}
}

type recordingQuestionWorder struct {
	facts holdrecord.QuestionFacts
}

func (questionWorder *recordingQuestionWorder) WordQuestion(_ context.Context, facts holdrecord.QuestionFacts) holdrecord.QuestionWording {
	questionWorder.facts = facts
	return holdrecord.QuestionWording{Text: "지울까요?"}
}

type interruptedPermissionAsker struct{}

func (interruptedPermissionAsker) AskPermission(context.Context, mcpserver.ApprovalRequest, approvalgate.PermissionQuestion) (approvalgate.ApprovalAnswer, approvalgate.AskStatus) {
	return approvalgate.ApprovalAnswer{}, approvalgate.AskInterrupted
}

func holdACallOn(t *testing.T, toolSet *toolcontract.ToolSet, taskRunID string) {
	t.Helper()
	if toolSet == nil {
		t.Fatal("expected the launch to hand the turn a tool set")
	}
	toolSet.AllowTestReplacement()
	if errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:               "test:draft_delete",
		Name:             "draft_delete",
		Description:      "draft_delete",
		Visibility:       toolcontract.ToolVisibilityModel,
		InputSchema:      json.RawMessage(`{"type":"object"}`),
		RequiresApproval: true,
		SideEffectClass:  toolcontract.ToolSideEffectStateChange,
		ResultContract:   &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("done", json.RawMessage(`{}`)), nil
	}); errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
	toolSet.WithAllowedToolNames(toolcontract.AppendUniqueStrings(toolSet.ListToolNames(), "draft_delete")).Invoke(toolcontract.WithTaskRunID(context.Background(), taskRunID), toolcontract.ToolInvocation{ToolName: "draft_delete", Input: json.RawMessage(`{}`)})
}
