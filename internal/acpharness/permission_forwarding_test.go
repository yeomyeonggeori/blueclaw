package acpharness

import (
	"context"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcatalogtrust"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type relayDouble struct {
	asked         []approvalgate.HarnessPermissionQuestion
	approvalAsked []mcpserver.ApprovalRequest
	outcome       acp.RequestPermissionOutcome
	isAnswered    bool
}

func (relay *relayDouble) AskHarnessPermission(_ context.Context, approvalRequest mcpserver.ApprovalRequest, question approvalgate.HarnessPermissionQuestion) (acp.RequestPermissionOutcome, approvalgate.AskStatus) {
	relay.asked = append(relay.asked, question)
	relay.approvalAsked = append(relay.approvalAsked, approvalRequest)
	if relay.isAnswered {
		return relay.outcome, approvalgate.AskAnswered
	}
	return relay.outcome, approvalgate.AskInterrupted
}

func selectedOutcome(optionID acp.PermissionOptionId) acp.RequestPermissionOutcome {
	return acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{Outcome: "selected", OptionId: optionID}}
}

func harnessOwnedToolRequest(title string) *acp.RequestPermissionRequest {
	return &acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{ToolCallId: "harness-call-1", Title: &title, RawInput: map[string]any{"command": "git push --force"}},
		Options: []acp.PermissionOption{
			{OptionId: "allow-once", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow"},
			{OptionId: "allow-always", Kind: acp.PermissionOptionKindAllowAlways, Name: "Always allow"},
			{OptionId: "reject-once", Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject"},
		},
	}
}

type permissionLedger struct {
	events []agentcontract.TaskEvent
}

func runTurnWithAgentAsking(t *testing.T, agent *externalAgent, relay approvalgate.HarnessPermissionAsker) permissionLedger {
	t.Helper()
	return runTurnWithTrust(t, agent, relay, toolcatalogtrust.Trust{})
}

func runTurnWithTrust(t *testing.T, agent *externalAgent, relay approvalgate.HarnessPermissionAsker, trust toolcatalogtrust.Trust) permissionLedger {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "push the branch")
	store := taskRunService
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), store)
	harness.UseToolCatalogTrust(trust)
	if relay != nil {
		harness.UsePermissionAsker(relay)
	}
	_, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID:   "person-1",
		ExistingTaskRunID:   taskRun.TaskRunID,
		Platform:            "mattermost",
		ConversationID:      "conversation-1",
		OriginReplyTargetID: "thread-1",
		Prompt:              "push the branch",
		WorkspaceRootPath:   t.TempDir(),
		ToolSet:             requesterToolSet(t, "person-1", &[]daemonExecutedTool{}),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return permissionLedger{events: permissionEvents(taskRunService.ListTaskEvent(taskRun.TaskRunID))}
}

func permissionEvents(taskEvents []agentcontract.TaskEvent) []agentcontract.TaskEvent {
	permission := []agentcontract.TaskEvent{}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == agentcontract.TaskEventHarnessToolPermitted || taskEvent.Name == agentcontract.TaskEventHarnessToolRefused {
			permission = append(permission, taskEvent)
		}
	}
	return permission
}

func TestAHarnessQuestionReachesTheRelayInTheConversationItWasAskedIn(t *testing.T) {
	relay := &relayDouble{outcome: selectedOutcome("allow-once"), isAnswered: true}
	agent := &externalAgent{toolNameToCall: "note_write", permissionRequest: harnessOwnedToolRequest("Force-push the branch to origin")}

	runTurnWithAgentAsking(t, agent, relay)

	if len(relay.asked) != 1 || relay.asked[0].Text != "Force-push the branch to origin" || len(relay.asked[0].Options) != 3 {
		t.Fatalf("expected the harness's own question and options to reach the relay, got %+v", relay.asked)
	}
	approvalRequest := relay.approvalAsked[0]
	if approvalRequest.Platform != "mattermost" || approvalRequest.ConversationID != "conversation-1" || approvalRequest.ReplyTargetID != "thread-1" || approvalRequest.TaskRunID == "" || approvalRequest.RequesterPersonID != "person-1" {
		t.Fatalf("the question must be asked in the conversation the turn came from, got %+v", approvalRequest)
	}
	if !strings.Contains(string(approvalRequest.ToolInput), "git push --force") {
		t.Fatalf("the relay is told what the call would do, got %s", approvalRequest.ToolInput)
	}
}

func TestTheOptionThePersonChoseIsWhatTheHarnessIsAnswered(t *testing.T) {
	relay := &relayDouble{outcome: selectedOutcome("allow-always"), isAnswered: true}
	agent := &externalAgent{toolNameToCall: "note_write", permissionRequest: harnessOwnedToolRequest("Force-push the branch to origin")}

	store := runTurnWithAgentAsking(t, agent, relay)

	if agent.permissionOutcome.Selected == nil || agent.permissionOutcome.Selected.OptionId != "allow-always" {
		t.Fatalf("expected the chosen option to return to the harness, got %+v", agent.permissionOutcome)
	}
	if len(store.events) != 1 || store.events[0].Name != agentcontract.TaskEventHarnessToolPermitted || !strings.Contains(store.events[0].Body, "allow_always") {
		t.Fatalf("expected the permitted call in the ledger, got %+v", store.events)
	}
}

func TestARejectionTheHarnessOffersIsReturnedAndRecordedAsRefused(t *testing.T) {
	relay := &relayDouble{outcome: selectedOutcome("reject-once"), isAnswered: true}
	agent := &externalAgent{toolNameToCall: "note_write", permissionRequest: harnessOwnedToolRequest("Force-push the branch to origin")}

	store := runTurnWithAgentAsking(t, agent, relay)

	if agent.permissionOutcome.Selected == nil || agent.permissionOutcome.Selected.OptionId != "reject-once" {
		t.Fatalf("expected the rejection to return to the harness, got %+v", agent.permissionOutcome)
	}
	if len(store.events) != 1 || store.events[0].Name != agentcontract.TaskEventHarnessToolRefused {
		t.Fatalf("expected the refusal in the ledger, got %+v", store.events)
	}
}

func TestAHarnessNobodyCanBeAskedForIsCancelledNotAllowed(t *testing.T) {
	relay := &relayDouble{isAnswered: false}
	agent := &externalAgent{toolNameToCall: "note_write", permissionRequest: harnessOwnedToolRequest("Force-push the branch to origin")}

	store := runTurnWithAgentAsking(t, agent, relay)

	if agent.permissionOutcome.Cancelled == nil {
		t.Fatalf("with nobody to ask the harness is cancelled, got %+v", agent.permissionOutcome)
	}
	if len(store.events) != 1 || store.events[0].Name != agentcontract.TaskEventHarnessToolRefused {
		t.Fatalf("expected the refusal in the ledger, got %+v", store.events)
	}
}

func TestAQuestionWithNoWordsIsNeverPutToThePerson(t *testing.T) {
	relay := &relayDouble{outcome: selectedOutcome("allow-once"), isAnswered: true}
	agent := &externalAgent{toolNameToCall: "note_write", permissionRequest: harnessOwnedToolRequest("  ")}

	runTurnWithAgentAsking(t, agent, relay)

	if len(relay.asked) != 0 || agent.permissionOutcome.Cancelled == nil {
		t.Fatalf("a question nobody can read is cancelled, asked %+v, answered %+v", relay.asked, agent.permissionOutcome)
	}
}

func TestAHarnessWithNoRelayIsAllowedAsItAlwaysWas(t *testing.T) {
	agent := &externalAgent{toolNameToCall: "note_write", permissionRequest: harnessOwnedToolRequest("Force-push the branch to origin")}

	runTurnWithAgentAsking(t, agent, nil)

	if agent.permissionOutcome.Selected == nil || agent.permissionOutcome.Selected.OptionId != "allow-always" {
		t.Fatalf("with no relay wired nothing changes, got %+v", agent.permissionOutcome)
	}
}

func TestTheSessionIsOpenedWithTheTrustTheHarnessDefinitionDeclares(t *testing.T) {
	agent := &externalAgent{toolNameToCall: "note_write"}
	declared := map[string]any{"claudeCode": map[string]any{"options": map[string]any{"allowedTools": []string{"mcp__blueclaw"}}}}

	runTurnWithTrust(t, agent, nil, toolcatalogtrust.Trust{SessionMeta: declared})

	options, isMap := agent.observedSessionMeta["claudeCode"].(map[string]any)["options"].(map[string]any)
	if !isMap {
		t.Fatalf("the session carried no declared trust: %+v", agent.observedSessionMeta)
	}
	if allowed, isList := options["allowedTools"].([]any); !isList || len(allowed) != 1 || allowed[0] != "mcp__blueclaw" {
		t.Fatalf("expected exactly what the definition declared, got %+v", options["allowedTools"])
	}
}

func TestAHarnessDefinitionWithNoTrustLineOpensTheSessionWithNoTrust(t *testing.T) {
	agent := &externalAgent{toolNameToCall: "note_write"}

	runTurnWithAgentAsking(t, agent, nil)

	if agent.observedSessionMeta != nil {
		t.Fatalf("expected no trust to be invented, got %+v", agent.observedSessionMeta)
	}
}

func TestABuiltInToolOfTheHarnessIsStillAskedAbout(t *testing.T) {
	relay := &relayDouble{outcome: selectedOutcome("allow-once"), isAnswered: true}
	agent := &externalAgent{toolNameToCall: "note_write", permissionRequest: harnessOwnedToolRequest("Bash")}

	runTurnWithAgentAsking(t, agent, relay)

	if len(relay.asked) != 1 {
		t.Fatalf("a tool the harness owns reaches the person, asked %d", len(relay.asked))
	}
}
