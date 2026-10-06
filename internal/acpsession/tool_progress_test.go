package acpsession

import (
	"context"
	"fmt"
	"slices"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type toolCallingLauncher struct {
	updates []acp.SessionUpdate
	reply   string
}

func (launcher *toolCallingLauncher) Launch(_ context.Context, request agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchResult, error) {
	for _, update := range launcher.updates {
		request.ToolCallObserver(update)
	}
	return agentruntime.TaskLaunchResult{TurnResult: agentcontract.AgentTurnResult{
		TaskRun:       agentcontract.TaskRun{TaskRunID: "run-1", Status: agentcontract.TaskStatusCompleted},
		FinishMessage: launcher.reply,
	}}, nil
}

func toolCallingSession(t *testing.T, launcher *toolCallingLauncher) (*recordingClient, func()) {
	t.Helper()
	client := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: launcher,
		Directory:    staticDirectory{},
		ReplyReader:  scriptedReader{},
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	return client, func() {
		promptInConversation(t, connection, sessionID, map[string]any{"conversationType": "dm"})
	}
}

func describeNotification(notification acp.SessionNotification) string {
	update := notification.Update
	switch {
	case update.ToolCall != nil:
		return fmt.Sprintf("tool_call %s %q %s", update.ToolCall.ToolCallId, update.ToolCall.Title, update.ToolCall.Status)
	case update.ToolCallUpdate != nil:
		return fmt.Sprintf("tool_call_update %s %s", update.ToolCallUpdate.ToolCallId, *update.ToolCallUpdate.Status)
	case update.AgentMessageChunk != nil:
		return "message " + update.AgentMessageChunk.Content.Text.Text
	}
	return "other"
}

func deliveriesOf(t *testing.T, notifications []acp.SessionNotification) []Delivery {
	t.Helper()
	deliveries := []Delivery{}
	for _, notification := range notifications {
		delivery, isNamed := deliveryNamedIn(notification.Meta)
		if !isNamed {
			t.Fatalf("%s carries no delivery meta", describeNotification(notification))
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries
}

func TestToolCallsOfAnyHarnessAreToldToTheClientInOrderBeforeTheFinalReplyUnderOneDelivery(t *testing.T) {
	launcher := &toolCallingLauncher{
		updates: []acp.SessionUpdate{
			acp.StartToolCall("call-1", "Read notes.md", acp.WithStartStatus(acp.ToolCallStatusInProgress)),
			acp.UpdateToolCall("call-1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted)),
			acp.StartToolCall("call-2", "Write out.md", acp.WithStartStatus(acp.ToolCallStatusInProgress)),
			acp.UpdateToolCall("call-2", acp.WithUpdateStatus(acp.ToolCallStatusCompleted)),
		},
		reply: "정리했습니다",
	}
	client, prompt := toolCallingSession(t, launcher)

	prompt()

	described := []string{}
	for _, notification := range client.notifications {
		described = append(described, describeNotification(notification))
	}
	expected := []string{
		`tool_call call-1 "Read notes.md" in_progress`,
		"tool_call_update call-1 completed",
		`tool_call call-2 "Write out.md" in_progress`,
		"tool_call_update call-2 completed",
		"message 정리했습니다",
	}
	if !slices.Equal(described, expected) {
		t.Fatalf("the client was told\n%q\nexpected\n%q", described, expected)
	}
	assertOneDeliveryClosedByTheLastNotification(t, deliveriesOf(t, client.notifications))
}

func assertOneDeliveryClosedByTheLastNotification(t *testing.T, deliveries []Delivery) {
	t.Helper()
	last := len(deliveries) - 1
	for index, delivery := range deliveries {
		if delivery.DeliveryID != deliveries[0].DeliveryID || delivery.ReplyTargetID != "reply-target-1" {
			t.Fatalf("notification %d carries %+v, expected the delivery %q at reply-target-1 of the first", index, delivery, deliveries[0].DeliveryID)
		}
		if delivery.IsFinal != (index == last) {
			t.Fatalf("notification %d has isFinal=%t, only the last notification closes the delivery", index, delivery.IsFinal)
		}
	}
}

func TestAToolThatFailsIsToldToTheClientAsFailed(t *testing.T) {
	launcher := &toolCallingLauncher{
		updates: []acp.SessionUpdate{
			acp.StartToolCall("call-1", "Read missing.md", acp.WithStartStatus(acp.ToolCallStatusInProgress)),
			acp.UpdateToolCall("call-1", acp.WithUpdateStatus(acp.ToolCallStatusFailed)),
		},
		reply: "찾지 못했습니다",
	}
	client, prompt := toolCallingSession(t, launcher)

	prompt()

	update := client.notifications[1].Update.ToolCallUpdate
	if update == nil || update.Status == nil || *update.Status != acp.ToolCallStatusFailed {
		t.Fatalf("the second notification is %s, expected a tool_call_update with status failed", describeNotification(client.notifications[1]))
	}
}
