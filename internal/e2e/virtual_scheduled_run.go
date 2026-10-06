package e2e

import (
	"context"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const (
	virtualDirectConversationID = "virtual-direct-message-1"
	virtualScheduleID           = "virtual-schedule-1"
)

type virtualPlatformAccounts []identity.PlatformAccountIdentity

func (accounts virtualPlatformAccounts) ListPlatformAccount() ([]identity.PlatformAccountIdentity, error) {
	return accounts, nil
}

func useVirtualDirectMessages(runtime *connectors.ConnectorRuntime, identityService *identity.IdentityService) {
	account := identity.PlatformAccountIdentity{Platform: "virtual", ExternalUserID: "user-1", Email: "sample@example.com", PersonID: virtualRequesterPersonID}
	identityService.RememberPlatformAccount(account)
	runtime.UseRequesterDirectMessages(func(context.Context, string, string) (string, string, error) {
		return virtualDirectConversationID, virtualDirectConversationID, nil
	}, virtualPlatformAccounts{account})
}

func (harness *VirtualSessionHarness) handleTurn(ctx context.Context, virtualTurn VirtualTurn, event connectors.PlatformInboundEvent) (connectors.ConnectorRuntimeResult, error) {
	if virtualTurn.RunsScheduledRun {
		return harness.startScheduledRun(ctx, virtualTurn)
	}
	return harness.handleInboundEvent(ctx, event)
}

func (harness *VirtualSessionHarness) startScheduledRun(ctx context.Context, virtualTurn VirtualTurn) (connectors.ConnectorRuntimeResult, error) {
	turnContext, stop := context.WithCancel(ctx)
	turn := &askingTurn{finished: make(chan handledInboundEvent, 1), stop: stop}
	go func() {
		result, errorValue := harness.runScheduledRun(turnContext, virtualTurn)
		turn.finished <- handledInboundEvent{result: result, errorValue: errorValue}
	}()
	return harness.untilFinishedOrAsking(ctx, turn)
}

func (harness *VirtualSessionHarness) runScheduledRun(ctx context.Context, virtualTurn VirtualTurn) (connectors.ConnectorRuntimeResult, error) {
	dueAt := time.Now().UTC().Add(-time.Minute)
	runResult, errorValue := harness.scheduleRunner.RunIfDue(ctx, agentruntime.ScheduleRunRequest{
		Schedule:      virtualScheduledRun(virtualTurn.Prompt, dueAt),
		ReferenceTime: dueAt.Add(time.Second),
		PersonAccess:  policy.PersonAccess{PersonID: virtualRequesterPersonID},
		WorkspaceID:   "e2e",
	})
	if errorValue != nil {
		return connectors.ConnectorRuntimeResult{}, errorValue
	}
	return connectors.ConnectorRuntimeResult{Handled: true, Platform: "virtual", TaskRunID: runResult.LaunchResult.TurnResult.TaskRun.TaskRunID}, nil
}

func virtualScheduledRun(prompt string, dueAt time.Time) task.Schedule {
	return task.Schedule{
		ScheduleID:       virtualScheduleID,
		CreatorPersonID:  virtualRequesterPersonID,
		Name:             "Scheduled message",
		Prompt:           prompt,
		ExecutionMode:    task.ScheduleExecutionModeAgent,
		AgentProfileName: "default",
		Platform:         "virtual",
		ConversationID:   virtualConversationID,
		ReplyTargetID:    virtualConversationID,
		TimeZone:         "Asia/Seoul",
		Kind:             task.ScheduleKindOnce,
		RunAt:            &dueAt,
		NextRunAt:        &dueAt,
	}
}
