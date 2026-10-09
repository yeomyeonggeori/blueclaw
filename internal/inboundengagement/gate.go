package inboundengagement

import (
	"context"
	"log/slog"
	"strings"
)

const ambientDutyLaunchConfidenceThreshold = 0.7

const attachmentsOnlyUninvitedReason = "attachments_only_uninvited"

type Decision struct {
	ShouldLaunch  bool
	ReactionEmoji string
	IgnoreReason  string
	AmbientDuty   AmbientDutyContext
}

type Request struct {
	MessageID        string
	ConversationType string
	BotMentioned     bool
	AttachmentsOnly  bool
}

const reactionOnlyReason = "reaction_only"

func Resolve(ctx context.Context, logger *slog.Logger, platform string, request Request, judge func(context.Context) (Judgment, error)) Decision {
	if IsIgnoredWithoutDeciding(request) {
		return Decision{IgnoreReason: attachmentsOnlyUninvitedReason}
	}
	judgment, errorValue := judge(ctx)
	if errorValue != nil {
		logger.Warn("connector."+platform+".addressing.decision_failed", slog.String("messageID", request.MessageID), slog.String("error", errorValue.Error()))
		if request.BotMentioned || !IsMultiPersonConversation(request.ConversationType) {
			return Decision{ShouldLaunch: true}
		}
		return Decision{IgnoreReason: "addressing_decision_failed dutyMatch=false"}
	}
	addressing := judgment.Addressing
	ambientDuty := ambientDutyContextFromAddressingDecision(addressing)
	if shouldLaunch(request, judgment, ambientDuty) {
		return Decision{ShouldLaunch: true, ReactionEmoji: addressing.ReactionEmoji, AmbientDuty: ambientDuty}
	}
	if addressing.ReactionEmoji != "" {
		return Decision{ReactionEmoji: addressing.ReactionEmoji, IgnoreReason: reactionOnlyReason}
	}
	return Decision{IgnoreReason: "addressing_" + string(addressing.Target) + " dutyMatch=false"}
}

func shouldLaunch(request Request, judgment Judgment, ambientDuty AmbientDutyContext) bool {
	if judgment.Addressing.ShouldRespond || ambientDuty.IsMatch {
		return true
	}
	return isAskedOfTheAgent(request, judgment.Addressing) && (judgment.Addressing.HasWork || continuesOpenWork(judgment))
}

func isAskedOfTheAgent(request Request, addressing AddressingDecision) bool {
	return !IsMultiPersonConversation(request.ConversationType) || request.BotMentioned || addressing.Target == AddressingTargetBot
}

func continuesOpenWork(judgment Judgment) bool {
	if judgment.HasRelatesToActiveTask && judgment.RelatesToActiveTask {
		return true
	}
	switch judgment.BusyRoute {
	case BusyRouteStatus, BusyRouteSteer, BusyRouteReplace, BusyRouteCancel:
		return true
	}
	return false
}

func IsIgnoredWithoutDeciding(request Request) bool {
	return IsMultiPersonConversation(request.ConversationType) && request.AttachmentsOnly && !request.BotMentioned
}

func ShouldIgnoreUninvitedAddressing(conversationType string, botMentioned bool) bool {
	return IsMultiPersonConversation(conversationType) && !botMentioned
}

func IsMultiPersonConversation(conversationType string) bool {
	normalizedConversationType := strings.ToLower(strings.TrimSpace(conversationType))
	if normalizedConversationType == "" {
		return false
	}
	switch normalizedConversationType {
	case "d", "dm", "im", "direct":
		return false
	}
	return true
}

func ambientDutyContextFromAddressingDecision(decision AddressingDecision) AmbientDutyContext {
	if !decision.DutyMatch || decision.DutyConfidence < ambientDutyLaunchConfidenceThreshold {
		return AmbientDutyContext{}
	}
	return (AmbientDutyContext{
		IsMatch:    true,
		Name:       decision.DutyName,
		Confidence: decision.DutyConfidence,
	}).Normalized()
}
