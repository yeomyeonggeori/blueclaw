package acpharness

import (
	"context"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func selectedOptionID(t *testing.T, options []acp.PermissionOption) acp.PermissionOptionId {
	t.Helper()
	response, errorValue := (&sessionObserver{}).RequestPermission(context.Background(), acp.RequestPermissionRequest{Options: options})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.Outcome.Selected == nil {
		t.Fatalf("expected an agent asking to act to be allowed, because POSIX permissions are the boundary, not this prompt; got %+v", response.Outcome)
	}
	return response.Outcome.Selected.OptionId
}

func TestAnAgentAskingToActIsAllowedBecauseTheKernelDecides(t *testing.T) {
	optionID := selectedOptionID(t, []acp.PermissionOption{
		{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce},
		{OptionId: "allow", Kind: acp.PermissionOptionKindAllowOnce},
	})

	if optionID != "allow" {
		t.Fatalf("expected the allow option, got %q", optionID)
	}
}

func TestAllowAlwaysIsPreferredSoOneToolDoesNotAskEveryStep(t *testing.T) {
	optionID := selectedOptionID(t, []acp.PermissionOption{
		{OptionId: "once", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "always", Kind: acp.PermissionOptionKindAllowAlways},
	})

	if optionID != "always" {
		t.Fatalf("expected the allow-always option, got %q", optionID)
	}
}

func TestAnAgentOfferingNoWayToProceedIsCancelledRatherThanLeftWaiting(t *testing.T) {
	response, errorValue := (&sessionObserver{}).RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce}},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.Outcome.Cancelled == nil {
		t.Fatalf("expected a cancelled outcome when no option lets the turn proceed, got %+v", response.Outcome)
	}
}

type recordingTaskRunStore struct {
	taskstate.TaskRunStore
	events []agentcontract.TaskEvent
}

func (store *recordingTaskRunStore) AppendTaskEvent(taskRunID string, name string, body string) {
	store.events = append(store.events, agentcontract.TaskEvent{TaskRunID: taskRunID, Name: name, Body: body})
}

func permissionRequestForHarnessOwnedTool(optionKind acp.PermissionOptionKind) acp.RequestPermissionRequest {
	title := "run a shell command"
	return acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{{OptionId: "allow", Kind: optionKind}},
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: "call-1",
			Title:      &title,
			RawInput:   map[string]any{"command": "rm -rf /workspace/private/people/person-1/drafts"},
		},
	}
}

func TestAToolTheHarnessRunsItselfIsRecordedEvenThoughItIsPermitted(t *testing.T) {
	store := &recordingTaskRunStore{}
	observer := &sessionObserver{taskRunStore: store, taskRunID: "task-1"}

	if _, errorValue := observer.RequestPermission(context.Background(), permissionRequestForHarnessOwnedTool(acp.PermissionOptionKindAllowOnce)); errorValue != nil {
		t.Fatalf("expected the permission to be answered: %v", errorValue)
	}

	if len(store.events) != 1 || store.events[0].Name != agentcontract.TaskEventHarnessToolPermitted {
		t.Fatalf("a call the catalog never saw must still reach the ledger, got %+v", store.events)
	}
	for _, expectedFragment := range []string{"run a shell command", "rm -rf /workspace/private/people/person-1/drafts", "allow_once"} {
		if !strings.Contains(store.events[0].Body, expectedFragment) {
			t.Fatalf("the ledger has to say what was permitted and how widely, expected %q in %s", expectedFragment, store.events[0].Body)
		}
	}
}

func TestAPermissionRequestWithNoAllowableOptionIsRecordedAsRefused(t *testing.T) {
	store := &recordingTaskRunStore{}
	observer := &sessionObserver{taskRunStore: store, taskRunID: "task-1"}

	if _, errorValue := observer.RequestPermission(context.Background(), permissionRequestForHarnessOwnedTool(acp.PermissionOptionKindRejectOnce)); errorValue != nil {
		t.Fatalf("expected the permission to be answered: %v", errorValue)
	}

	if len(store.events) != 1 || store.events[0].Name != agentcontract.TaskEventHarnessToolRefused {
		t.Fatalf("expected the refusal to be recorded, got %+v", store.events)
	}
}

func TestATurnWithNoTaskRunRecordsNothingRatherThanFailing(t *testing.T) {
	observer := &sessionObserver{}

	if _, errorValue := observer.RequestPermission(context.Background(), permissionRequestForHarnessOwnedTool(acp.PermissionOptionKindAllowOnce)); errorValue != nil {
		t.Fatalf("expected the permission to be answered without a ledger: %v", errorValue)
	}
}
