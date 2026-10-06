package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
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

type askingWithoutAnEvent struct {
	fixture  *threadAskFixture
	answers  chan approvalgate.ApprovalAnswer
	statuses chan approvalgate.AskStatus
	taskRun  task.TaskRun
}

func askWithoutAnEvent(t *testing.T, fixture *threadAskFixture) *askingWithoutAnEvent {
	t.Helper()
	fixture.connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})
	asking := &askingWithoutAnEvent{
		fixture:  fixture,
		answers:  make(chan approvalgate.ApprovalAnswer, 1),
		statuses: make(chan approvalgate.AskStatus, 1),
		taskRun:  fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "schedule:morning"}, "scheduled run"),
	}
	approvalRequest := mcpserver.ApprovalRequest{RequesterPersonID: "person-1", TaskRunID: asking.taskRun.TaskRunID, Platform: "test", ConversationID: "schedule:morning"}
	go func() {
		answer, status := fixture.connectorRuntime.ThreadPermissionAsker().AskPermission(context.Background(), approvalRequest, approvalgate.PermissionQuestion{HoldID: "hold-1", Confirmation: "내일 휴가 일정을 삭제할까요?"})
		asking.statuses <- status
		if status == approvalgate.AskAnswered {
			asking.answers <- answer
		}
		close(asking.answers)
	}()
	return asking
}

func (asking *askingWithoutAnEvent) awaitAnswer(t *testing.T) (approvalgate.ApprovalAnswer, bool) {
	t.Helper()
	select {
	case answer, isAnswered := <-asking.answers:
		return answer, isAnswered
	case <-time.After(10 * time.Second):
		t.Fatal("the asker was never answered")
		return approvalgate.ApprovalAnswer{}, false
	}
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
	return event
}

func TestARunWithoutAnEventAsksInTheRequestersDirectMessageAndIsAnsweredThere(t *testing.T) {
	fixture := newThreadAskFixture(t, threadAskScript{approvalReplies: []string{`{"answer":"approve"}`}})
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	asking := askWithoutAnEvent(t, fixture)
	fixture.awaitQuestionOnTheThread(t)

	answered := fixture.await(t, fixture.send(context.Background(), directMessageReply("message-2", "ㅇ")))
	answer, isAnswered := asking.awaitAnswer(t)

	if answered.Reason != ApprovalAnsweredInThreadReason || answered.TaskRunID != asking.taskRun.TaskRunID {
		t.Fatalf("the reply settled %+v, expected the waiting run's question", answered)
	}
	if !isAnswered || answer.Signal != "approve" {
		t.Fatalf("the asker heard %+v answered=%v, expected an approval", answer, isAnswered)
	}
	if fixture.questionsPosted() != 1 || fixture.adapter.sentReplies[0].target.ConversationID != requesterDirectConversationID {
		t.Fatalf("questions %d posted to %+v, expected one in %s", fixture.questionsPosted(), fixture.adapter.sentReplies, requesterDirectConversationID)
	}
}

func TestAReplyInTheQuestionsThreadInTheDirectMessageAnswersToo(t *testing.T) {
	fixture := newThreadAskFixture(t, threadAskScript{approvalReplies: []string{`{"answer":"approve"}`}})
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	asking := askWithoutAnEvent(t, fixture)
	fixture.awaitQuestionOnTheThread(t)

	fixture.await(t, fixture.send(context.Background(), replyInTheQuestionsThread("message-2", "ㅇ")))

	if answer, isAnswered := asking.awaitAnswer(t); !isAnswered || answer.Signal != "approve" {
		t.Fatalf("the asker heard %+v answered=%v, expected an approval", answer, isAnswered)
	}
}

func TestAReplyInAnotherConversationIsNotOfferedTheDirectMessageQuestion(t *testing.T) {
	fixture := newThreadAskFixture(t, threadAskScript{approvalReplies: []string{`{"answer":"approve"}`}})
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	askWithoutAnEvent(t, fixture)
	fixture.awaitQuestionOnTheThread(t)

	if offered := fixture.connectorRuntime.askingThreads.awaiting("test", "channel-1", "person-1"); len(offered) != 0 {
		t.Fatalf("a reply in another conversation was offered %d questions", len(offered))
	}
}

func TestARunWithoutAnEventAndWithoutADirectMessageAsksNothing(t *testing.T) {
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

			if status := <-asking.statuses; status != approvalgate.AskUnreachable || fixture.questionsPosted() != 0 {
				t.Fatalf("status %q with %d questions posted, expected a requester who cannot be asked to be reported unreachable", status, fixture.questionsPosted())
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
		ApprovalScope:    "calendar",
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

func TestAForeignHarnessCallThroughTheCatalogServerIsAskedInTheDirectMessageNotParked(t *testing.T) {
	fixture := newThreadAskFixture(t, threadAskScript{approvalReplies: []string{`{"answer":"approve"}`}, turnRouters: nil})
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	fixture.connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})
	taskRun := fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1"}, threadAskRequest)
	invokedCount := &atomic.Int32{}
	session := connectedCatalogSessionGatedBy(t, fixture, taskRun.TaskRunID, invokedCount)
	called := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "calendar_event_delete", Arguments: map[string]any{"eventHint": "event-1"}})
		called <- result
	}()
	fixture.awaitQuestionOnTheThread(t)

	fixture.await(t, fixture.send(context.Background(), directMessageReply("message-2", "ㅇ")))

	select {
	case result := <-called:
		if result == nil || result.IsError || invokedCount.Load() != 1 {
			t.Fatalf("the harness call ended %+v with %d runs, expected one run after the approval", result, invokedCount.Load())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the harness call never returned")
	}
	finished, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(taskRun.TaskRunID)
	if finished.Status == task.TaskStatusWaitingApproval {
		t.Fatalf("the run was left %s, expected the call to carry on in place", finished.Status)
	}
}
