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
	SettledCalls              []agentcontract.CarriedOutCall
	ActiveGoal                agentcontract.ActiveGoal
	HasActiveGoal             bool
	PriorTask                 agentcontract.PriorTaskContext
	PrecomputedTurnDecision   *agentcontract.TurnDecision
	DecidedTurnFields         *agentcontract.TurnDecision
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
	return withTurnContinuation(connectorRuntime.withTurnMessage(agentruntime.TaskLaunchRequest{
		Source:                    agentruntime.TaskLaunchSourceConnector,
		SourceReference:           event.DedupeKey(),
		RequesterPersonID:         turn.RequesterPersonID,
		RequesterName:             connectorRuntime.requesterNameForEvent(turn.RequesterPersonID, event),
		RequesterCallingName:      event.Context.Sender.CallingName,
		RequesterHandle:           event.Context.Sender.Handle,
		RequesterEmail:            turn.RequesterEmail,
		RequesterPlatformUserID:   event.SenderID,
		OriginReplyTargetID:       event.ReplyTargetID,
		OriginIsThread:            eventIsThreadReply(event),
		ProfileName:               "default",
		Platform:                  turn.Platform,
		ConversationID:            event.ConversationID,
		ConversationType:          event.Context.ConversationType,
		ConversationChannelID:     event.Context.ChannelID,
		ConversationChannelName:   event.Context.ChannelName,
		ReplyTargetID:             event.ReplyTargetID,
		Prompt:                    promptForTurn(turn),
		ResponseLanguage:          responseLanguageForEvent(event),
		DecidedTurnFields:         turn.DecidedTurnFields,
		AmbientDuty:               turn.AmbientDuty,
		HistoryProvider:           connectorHistoryProvider{adapter: turn.Adapter},
		PersonAccess:              turn.PersonAccess,
		AccessibleConversationIDs: turn.AccessibleConversationIDs,
		CheckpointSender:          checkpointSender,
	}, turn), turn)
}

func (connectorRuntime *ConnectorRuntime) withTurnMessage(request agentruntime.TaskLaunchRequest, turn ConversationTurn) agentruntime.TaskLaunchRequest {
	request.InputParts = append([]agentcontract.AgentPart{}, turn.Event.InputParts...)
	request.VisibleContext = turn.Event.Context.ToAgentVisibleContext()
	request.AttachmentMaterialResolver = connectorRuntime.attachmentMaterialResolverFor(turn.Adapter, turn.RequesterPersonID, turn.Event)
	return request
}

func withTurnContinuation(request agentruntime.TaskLaunchRequest, turn ConversationTurn) agentruntime.TaskLaunchRequest {
	request.IsApprovalContinuation = turn.IsApprovalContinuation
	request.SettledCalls = turn.SettledCalls
	request.IsRuntimeRestartResume = turn.IsBlockedContinuation
	request.ExistingTaskRunID = existingGoalTaskRunIDFromTurn(turn)
	request.ActiveGoal = activeGoalForLaunch(turn.ActiveGoal, turn.HasActiveGoal)
	request.PriorTask = turn.PriorTask
	request.PrecomputedTurnDecision = turnDecisionForTurn(turn)
	return request
}

func eventIsThreadReply(event PlatformInboundEvent) bool {
	if event.IsThread != nil {
		return *event.IsThread
	}
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
