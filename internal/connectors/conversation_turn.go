package connectors

import (
	"context"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type ConversationTurn struct {
	Platform                  string
	Adapter                   PlatformAdapter
	Event                     PlatformInboundEvent
	ReplyTarget               ReplyTarget
	RequesterPersonID         string
	RequesterEmail            string
	PersonAccess              policy.PersonAccess
	SettledCalls              []agentcontract.CarriedOutCall
	ActiveGoal                agentcontract.ActiveGoal
	HasActiveGoal             bool
	PriorTask                 agentcontract.PriorTaskContext
	PendingInput              agentcontract.PendingInputContext
	AmbientDuty               inboundengagement.AmbientDutyContext
	CheckpointSender          agentcontract.AgentCheckpointSender
	AccessibleConversationIDs []string
	IsBlockedContinuation     bool
}

func ambientDutyForTurn(turn ConversationTurn) (inboundengagement.StandingDuty, bool) {
	duty, isKnownDuty := inboundengagement.StandingDutyByName(turn.AmbientDuty.Name)
	return duty, turn.AmbientDuty.IsMatch && isKnownDuty
}

const ambientDutyTaskLevel = agentcontract.TaskLevelLow

func taskLevelForTurn(turn ConversationTurn) agentcontract.TaskLevel {
	if _, isAmbientDuty := ambientDutyForTurn(turn); isAmbientDuty {
		return ambientDutyTaskLevel
	}
	return ""
}

func pendingInputOf(turn *inboundTurn) agentcontract.PendingInputContext {
	if !turn.hasPendingAskInteraction {
		return agentcontract.PendingInputContext{}
	}
	ask := turn.pendingAskInteraction
	return agentcontract.PendingInputContext{
		TaskRunID:     ask.TaskRunID,
		Question:      ask.Question,
		SelectionMode: ask.SelectionMode,
		Options:       choiceReplyOptions(ask.Options),
	}
}

func (connectorRuntime *ConnectorRuntime) buildTaskLaunchRequest(turn ConversationTurn) agentruntime.TaskLaunchRequest {
	event := turn.Event
	return withTurnContinuation(agentruntime.TaskLaunchRequest{
		Source:                     agentruntime.TaskLaunchSourceConnector,
		SourceReference:            event.DedupeKey(),
		RequesterPersonID:          turn.RequesterPersonID,
		RequesterName:              connectorRuntime.requesterNameForEvent(turn.RequesterPersonID, event),
		RequesterCallingName:       event.Context.Sender.CallingName,
		RequesterHandle:            event.Context.Sender.Handle,
		RequesterEmail:             turn.RequesterEmail,
		RequesterPlatformUserID:    event.SenderID,
		OriginReplyTargetID:        event.ReplyTargetID,
		OriginIsThread:             eventIsThreadReply(event),
		ProfileName:                "default",
		Platform:                   turn.Platform,
		ConversationID:             event.ConversationID,
		ConversationType:           event.Context.ConversationType,
		ConversationChannelID:      event.Context.ChannelID,
		ConversationChannelName:    event.Context.ChannelName,
		ReplyTargetID:              event.ReplyTargetID,
		Prompt:                     event.Prompt,
		InputParts:                 append([]agentcontract.AgentPart{}, event.InputParts...),
		ResponseLanguage:           responseLanguageForEvent(event),
		VisibleContext:             event.Context.ToAgentVisibleContext(),
		HistoryProvider:            connectorHistoryProvider{adapter: turn.Adapter},
		AttachmentMaterialResolver: connectorRuntime.attachmentMaterialResolverFor(turn.Adapter, turn.RequesterPersonID, event),
		PersonAccess:               turn.PersonAccess,
		AccessibleConversationIDs:  turn.AccessibleConversationIDs,
		CheckpointSender:           turn.CheckpointSender,
	}, turn)
}

func withTurnContinuation(request agentruntime.TaskLaunchRequest, turn ConversationTurn) agentruntime.TaskLaunchRequest {
	request.SettledCalls = turn.SettledCalls
	request.IsRuntimeRestartResume = turn.IsBlockedContinuation
	request.ExistingTaskRunID = existingGoalTaskRunIDFromTurn(turn)
	request.ActiveGoal = activeGoalForLaunch(turn.ActiveGoal, turn.HasActiveGoal)
	request.PriorTask = turn.PriorTask
	request.PendingInput = turn.PendingInput
	request.TaskLevel = taskLevelForTurn(turn)
	return withAmbientDuty(request, turn)
}

func withAmbientDuty(request agentruntime.TaskLaunchRequest, turn ConversationTurn) agentruntime.TaskLaunchRequest {
	request.AmbientDuty = turn.AmbientDuty
	duty, isAmbientDuty := ambientDutyForTurn(turn)
	if !isAmbientDuty {
		return request
	}
	request.Prompt = inboundengagement.AmbientDutyInstructionPrompt(duty, turn.Event.Prompt, turn.Event.Context.Sender.Name)
	request.CheckpointSender = nil
	return request
}

func eventIsThreadReply(event PlatformInboundEvent) bool {
	if event.IsThread != nil {
		return *event.IsThread
	}
	return event.ReplyTargetID != "" && event.MessageID != "" && event.ReplyTargetID != event.MessageID
}

func existingGoalTaskRunIDFromTurn(turn ConversationTurn) string {
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
