//go:build !nobundledharness

package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const requesterDirectConversationID = "direct-message-1"

type fixedPlatformAccounts []identity.PlatformAccountIdentity

func (accounts fixedPlatformAccounts) ListPlatformAccount() ([]identity.PlatformAccountIdentity, error) {
	return accounts, nil
}

func requesterAccountsOnThePlatform() fixedPlatformAccounts {
	return fixedPlatformAccounts{{Platform: "test", ExternalUserID: "sender-user", PersonID: "person-1"}}
}

func openRequesterDirectMessage(context.Context, string, string) (string, string, error) {
	return requesterDirectConversationID, requesterDirectConversationID, nil
}

type runWithoutAnEvent struct {
	fixture *threadAskFixture
	taskRun task.TaskRun
	outcome mcpserver.ApprovalOutcome
}

func scheduledRunOn(fixture *threadAskFixture) *runWithoutAnEvent {
	fixture.connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})
	return &runWithoutAnEvent{
		fixture: fixture,
		taskRun: fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "schedule:morning"}, "scheduled run"),
	}
}

func (run *runWithoutAnEvent) callHeldTool(t *testing.T, toolName string, toolInput string) *runWithoutAnEvent {
	t.Helper()
	outcome, errorValue := run.fixture.connectorRuntime.approvalGate.AwaitApproval(context.Background(), mcpserver.ApprovalRequest{
		RequesterPersonID: "person-1",
		TaskRunID:         run.taskRun.TaskRunID,
		Platform:          "test",
		ConversationID:    "schedule:morning",
		ToolName:          toolName,
		ToolInput:         json.RawMessage(toolInput),
	})
	if errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	run.outcome = outcome
	return run
}

func (run *runWithoutAnEvent) hold(t *testing.T) holdrecord.Hold {
	t.Helper()
	holds := run.fixture.holds(run.taskRun.TaskRunID)
	if len(holds) == 0 {
		t.Fatal("the run holds no call")
	}
	return holds[len(holds)-1]
}

func (run *runWithoutAnEvent) answer(event PlatformInboundEvent) ParkedAnswer {
	sessionTurn := run.fixture.connectorRuntime.OpenSessionTurn(context.Background(), event, "person-1", run.fixture.adapter.SendReply)
	return sessionTurn.AnswerParkedQuestion(context.Background())
}

func askWithoutAnEvent(t *testing.T, fixture *threadAskFixture) *runWithoutAnEvent {
	t.Helper()
	return scheduledRunOn(fixture).callHeldTool(t, "event_delete", `{"eventHint":"event-1"}`)
}

func directMessageReply(messageID string, prompt string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = requesterDirectConversationID
	event.ReplyTargetID = messageID
	event.Prompt = prompt
	return event
}

func replyInTheQuestionsThread(messageID string, prompt string) PlatformInboundEvent {
	event := directMessageReply(messageID, prompt)
	event.ReplyTargetID = requesterDirectConversationID + ":dispatch-1"
	isThread := true
	event.IsThread = &isThread
	return event
}

func TestARunWithoutAnEventParksOnAQuestionInTheRequestersDirectMessageAndIsAnsweredThere(t *testing.T) {
	fixture := newThreadAskFixture(t, threadAskScript{approvalReplies: []string{`{"answer":"approve"}`}})
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	asking := askWithoutAnEvent(t, fixture)

	answer := asking.answer(directMessageReply("message-2", "ㅇ"))

	if asking.outcome.Decision != mcpserver.ApprovalDecisionHeld || fixture.questionsPosted() != 1 || fixture.adapter.sentReplies[0].target.ConversationID != requesterDirectConversationID {
		t.Fatalf("outcome %q with %d questions posted to %+v, expected the run parked on one question in %s", asking.outcome.Decision, fixture.questionsPosted(), fixture.adapter.sentReplies, requesterDirectConversationID)
	}
	if !answer.ShouldResume || answer.TaskRun.TaskRunID != asking.taskRun.TaskRunID || asking.hold(t).State != holdrecord.StateApproved {
		t.Fatalf("the reply settled %+v with the hold %s, expected the parked run approved", answer, asking.hold(t).State)
	}
}

func TestAReplyInTheQuestionsThreadInTheDirectMessageAnswersToo(t *testing.T) {
	fixture := newThreadAskFixture(t, threadAskScript{approvalReplies: []string{`{"answer":"approve"}`}})
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	asking := askWithoutAnEvent(t, fixture)

	answer := asking.answer(replyInTheQuestionsThread("message-2", "ㅇ"))

	if !answer.ShouldResume || asking.hold(t).State != holdrecord.StateApproved {
		t.Fatalf("a reply in the thread under the question settled %+v, expected an approval", answer)
	}
}

func TestAReplyInAnotherConversationIsNotOfferedTheDirectMessageQuestion(t *testing.T) {
	fixture := newThreadAskFixture(t, threadAskScript{approvalReplies: []string{`{"answer":"approve"}`}})
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	asking := askWithoutAnEvent(t, fixture)

	answer := asking.answer(threadReplyEvent("message-2", "ㅇ"))

	if answer.IsAnswer || asking.hold(t).State != holdrecord.StatePending {
		t.Fatalf("a reply in another conversation settled %+v", answer)
	}
}

func TestARunWithoutAnEventAndWithoutADirectMessageIsRefusedAndNeverParked(t *testing.T) {
	for label, opener := range map[string]DirectMessageOpener{
		"no direct message opener": nil,
		"a direct message that cannot be opened": func(context.Context, string, string) (string, string, error) {
			return "", "", errors.New("chatd is down")
		},
	} {
		t.Run(label, func(t *testing.T) {
			fixture := newThreadAskFixture(t, threadAskScript{})
			fixture.connectorRuntime.UseRequesterDirectMessages(opener, requesterAccountsOnThePlatform())

			asking := askWithoutAnEvent(t, fixture)

			if asking.outcome.Decision != mcpserver.ApprovalDecisionUnanswerable || fixture.questionsPosted() != 0 || fixture.taskRun(t, asking.taskRun.TaskRunID).Status == task.TaskStatusWaitingApproval {
				t.Fatalf("outcome %q with %d questions posted, expected a requester who cannot be asked to be reported unreachable", asking.outcome.Decision, fixture.questionsPosted())
			}
		})
	}
}

func connectedCatalogSessionGatedBy(t *testing.T, fixture *threadAskFixture, taskRunID string, invokedCount *atomic.Int32) *mcp.ClientSession {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"calendar_event_delete"})
	toolSet.AllowTestReplacement()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:               "test:calendar_event_delete",
		Name:             "calendar_event_delete",
		Description:      "Deletes a calendar event.",
		Visibility:       toolcontract.ToolVisibilityModel,
		InputSchema:      json.RawMessage(`{"type":"object","properties":{"eventHint":{"type":"string"}}}`),
		SideEffectClass:  toolcontract.ToolSideEffectStateChange,
		RequiresApproval: true,
		ResultContract:   &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		invokedCount.Add(1)
		return toolcontract.ToolSuccessData("deleted", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	toolSet.UseToolCallGate(fixture.connectorRuntime.approvalGate.TurnGate(approvalgate.TurnContext{RequesterPersonID: "person-1"}))
	server, errorValue := mcpserver.NewToolCatalogServer(mcpserver.RequesterToolSet{RequesterPersonID: "person-1", TaskRunID: taskRunID, ToolSet: toolSet}, "test")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = server.Run(context.Background(), serverTransport) }()
	session, errorValue := mcp.NewClient(&mcp.Implementation{Name: "foreign-harness", Version: "test"}, nil).Connect(context.Background(), clientTransport, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestAForeignHarnessCallThroughTheCatalogServerParksAndRunsOnceWhenCalledAgainAfterTheApproval(t *testing.T) {
	fixture := newThreadAskFixture(t, threadAskScript{approvalReplies: []string{`{"answer":"approve"}`}, turnRouters: nil})
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	fixture.connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})
	taskRun := fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1"}, threadAskRequest)
	invokedCount := &atomic.Int32{}
	session := connectedCatalogSessionGatedBy(t, fixture, taskRun.TaskRunID, invokedCount)
	call := &mcp.CallToolParams{Name: "calendar_event_delete", Arguments: map[string]any{"eventHint": "event-1"}}

	held, errorValue := session.CallTool(context.Background(), call)

	if errorValue != nil || held == nil || !held.IsError || invokedCount.Load() != 0 {
		t.Fatalf("the first call ended %+v %v with %d runs, expected it held without running", held, errorValue, invokedCount.Load())
	}
	if fixture.taskRun(t, taskRun.TaskRunID).Status != task.TaskStatusWaitingApproval || fixture.questionsPosted() != 1 {
		t.Fatalf("the held call left the run %s with %d questions, expected it parked on one", fixture.taskRun(t, taskRun.TaskRunID).Status, fixture.questionsPosted())
	}
	answer := fixture.connectorRuntime.OpenSessionTurn(context.Background(), directMessageReply("message-2", "ㅇ"), "person-1", fixture.adapter.SendReply).AnswerParkedQuestion(context.Background())
	if !answer.ShouldResume {
		t.Fatalf("the approval settled %+v, expected the parked run to be resumed", answer)
	}

	approved, errorValue := session.CallTool(context.Background(), call)

	if errorValue != nil || approved == nil || approved.IsError || invokedCount.Load() != 1 {
		t.Fatalf("the call after the approval ended %+v %v with %d runs, expected one run", approved, errorValue, invokedCount.Load())
	}
}
