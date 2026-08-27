package connectors

import (
	"context"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type ConversationTurn struct {
	Platform                  string
	Adapter                   PlatformAdapter
	Event                     PlatformInboundEvent
	ReplyTarget               ReplyTarget
	RequesterPersonID         string
	RequesterEmail            string
	PersonAccess              policy.PersonAccess
	IsApprovalContinuation    bool
	ActiveGoal                agentcontract.ActiveGoal
	HasActiveGoal             bool
	PriorTask                 agentcontract.PriorTaskContext
	PrecomputedTurnDecision   *agentcontract.TurnDecision
	AmbientDuty               agentcontract.AmbientDutyContext
	CheckpointSender          agentcontract.AgentCheckpointSender
	AccessibleConversationIDs []string
	IsBlockedContinuation     bool
}

func ambientDutyForTurn(turn ConversationTurn) (agentcontract.StandingDuty, bool) {
	duty, isKnownDuty := agentcontract.StandingDutyByName(turn.AmbientDuty.Name)
	return duty, turn.AmbientDuty.IsMatch && isKnownDuty
}

func promptForTurn(turn ConversationTurn) string {
	duty, isAmbientDuty := ambientDutyForTurn(turn)
	if !isAmbientDuty {
		return turn.Event.Prompt
	}
	return agentcontract.AmbientDutyInstructionPrompt(duty, turn.Event.Prompt, turn.Event.Context.Sender.Name)
}

func turnDecisionForTurn(turn ConversationTurn) *agentcontract.TurnDecision {
	duty, isAmbientDuty := ambientDutyForTurn(turn)
	if !isAmbientDuty || turn.PrecomputedTurnDecision != nil {
		return turn.PrecomputedTurnDecision
	}
	turnDecision := agentcontract.AmbientDutyTurnDecision(duty, responseLanguageForEvent(turn.Event))
	return &turnDecision
}

func (connectorRuntime *ConnectorRuntime) buildTaskLaunchRequest(turn ConversationTurn) agentruntime.TaskLaunchRequest {
	event := turn.Event
	checkpointSender := turn.CheckpointSender
	if turn.AmbientDuty.IsMatch {
		checkpointSender = nil
	}
	attachmentMaterialResolver := connectorAttachmentMaterialResolver{
		adapter:          turn.Adapter,
		personID:         turn.RequesterPersonID,
		event:            event,
		sentSources:      connectorRuntime.sentAttachmentSources,
		attachmentWriter: connectorRuntime.attachmentWriterFor(turn.RequesterPersonID),
	}
	return agentruntime.TaskLaunchRequest{
		Source:                     agentruntime.TaskLaunchSourceConnector,
		SourceReference:            event.DedupeKey(),
		RequesterPersonID:          turn.RequesterPersonID,
		RequesterName:              connectorRuntime.requesterNameForEvent(turn.RequesterPersonID, event),
		RequesterCallingName:       event.Context.Sender.CallingName,
		RequesterHandle:            event.Context.Sender.Handle,
		RequesterEmail:             turn.RequesterEmail,
		RequesterPlatformUserID:    event.SenderID,
		IsApprovalContinuation:     turn.IsApprovalContinuation,
		IsRuntimeRestartResume:     turn.IsBlockedContinuation,
		ExistingTaskRunID:          existingGoalTaskRunIDFromTurn(turn),
		OriginReplyTargetID:        event.ReplyTargetID,
		OriginIsThread:             eventIsThreadReply(event),
		ProfileName:                "default",
		Platform:                   turn.Platform,
		ConversationID:             event.ConversationID,
		ConversationType:           event.Context.ConversationType,
		ConversationChannelID:      event.Context.ChannelID,
		ConversationChannelName:    event.Context.ChannelName,
		ReplyTargetID:              event.ReplyTargetID,
		Prompt:                     promptForTurn(turn),
		InputParts:                 append([]agentcontract.AgentPart{}, event.InputParts...),
		ResponseLanguage:           responseLanguageForEvent(event),
		VisibleContext:             event.Context.ToAgentVisibleContext(),
		ActiveGoal:                 activeGoalForLaunch(turn.ActiveGoal, turn.HasActiveGoal),
		PriorTask:                  turn.PriorTask,
		PrecomputedTurnDecision:    turnDecisionForTurn(turn),
		AmbientDuty:                turn.AmbientDuty,
		HistoryProvider:            connectorHistoryProvider{adapter: turn.Adapter},
		AttachmentMaterialResolver: attachmentMaterialResolver,
		PersonAccess:               turn.PersonAccess,
		MemoryNamespaces:           connectorRuntime.accessibleNamespaces(turn.RequesterPersonID, turn.PersonAccess, event),
		AccessibleConversationIDs:  turn.AccessibleConversationIDs,
		CheckpointSender:           checkpointSender,
	}
}

func eventIsThreadReply(event PlatformInboundEvent) bool {
	return event.ReplyTargetID != "" && event.MessageID != "" && event.ReplyTargetID != event.MessageID
}

func existingGoalTaskRunIDFromTurn(turn ConversationTurn) string {
	if turn.IsApprovalContinuation {
		return turn.ActiveGoal.TaskRunID
	}
	if turn.HasActiveGoal {
		return turn.ActiveGoal.TaskRunID
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) checkpointSenderForTurn(platform string, event PlatformInboundEvent, replyTarget ReplyTarget, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) agentcontract.AgentCheckpointSender {
	return func(checkpointContext context.Context, checkpoint agentcontract.AgentCheckpoint) error {
		return connectorRuntime.sendCheckpointReply(checkpointContext, platform, event, replyTarget, checkpoint, sendReply)
	}
}
