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
	SuppressReply bool
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

func Resolve(ctx context.Context, logger *slog.Logger, platform string, request Request, decideAddressing func(context.Context) (AddressingDecision, error)) Decision {
	if !IsMultiPersonConversation(request.ConversationType) {
		return Decision{ShouldLaunch: true}
	}
	if IsIgnoredWithoutDeciding(request) {
		return Decision{IgnoreReason: attachmentsOnlyUninvitedReason}
	}
	addressingDecision, errorValue := decideAddressing(ctx)
	if errorValue != nil {
		logger.Warn("connector."+platform+".addressing.decision_failed", slog.String("messageID", request.MessageID), slog.String("error", errorValue.Error()))
		if request.BotMentioned {
			return Decision{ShouldLaunch: true}
		}
		return Decision{IgnoreReason: "addressing_decision_failed dutyMatch=false"}
	}
	ambientDuty := ambientDutyContextFromAddressingDecision(addressingDecision)
	shouldLaunch := addressingDecision.ShouldRespond || ambientDuty.IsMatch
	if !shouldLaunch && addressingDecision.ReactionEmoji == "" {
		return Decision{IgnoreReason: "addressing_" + string(addressingDecision.Target) + " dutyMatch=false"}
	}
	return Decision{
		ShouldLaunch:  shouldLaunch,
		SuppressReply: AmbientDutyLaunchesWithoutReply(addressingDecision),
		ReactionEmoji: addressingDecision.ReactionEmoji,
		AmbientDuty:   ambientDuty,
	}
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

func AmbientDutyLaunchesWithoutReply(decision AddressingDecision) bool {
	return !decision.ShouldRespond && ambientDutyContextFromAddressingDecision(decision).IsMatch
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
