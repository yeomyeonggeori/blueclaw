package connectors

import (
	"context"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const ambientDutyLaunchConfidenceThreshold = 0.7

type inboundEngagementDecision struct {
	ShouldLaunch  bool
	SuppressReply bool
	ReactionEmoji string
	IgnoreReason  string
	AmbientDuty   agentcontract.AmbientDutyContext
}

func shouldIgnoreUninvitedAddressing(event PlatformInboundEvent) bool {
	return isMultiPersonConversation(event) && !event.Context.Addressing.BotMentioned
}

func (connectorRuntime *ConnectorRuntime) resolveInboundEngagement(ctx context.Context, platform string, event PlatformInboundEvent) inboundEngagementDecision {
	if !isMultiPersonConversation(event) {
		return inboundEngagementDecision{ShouldLaunch: true}
	}
	botMentioned := event.Context.Addressing.BotMentioned
	if event.Context.AttachmentsOnly && !botMentioned {
		return inboundEngagementDecision{IgnoreReason: "attachments_only_uninvited"}
	}
	addressingDecision, errorValue := connectorRuntime.intakeClassifier.ClassifyAddressing(ctx, agentcontract.AddressingClassificationRequest{
		Prompt:           event.Prompt,
		AgentIdentity:    connectorRuntime.agentIdentity(),
		BotMentioned:     botMentioned,
		MessageSentAt:    event.RawReceivedAt,
		ConversationType: event.Context.ConversationType,
		SenderName:       event.Context.Sender.Name,
		SenderHandle:     event.Context.Sender.Handle,
		VisibleContext:   event.Context.ToAgentVisibleContext(),
	})
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".addressing.classifier_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		if botMentioned {
			return inboundEngagementDecision{ShouldLaunch: true}
		}
		return inboundEngagementDecision{IgnoreReason: "addressing_classifier_failed dutyMatch=false"}
	}
	ambientDuty := ambientDutyContextFromAddressingDecision(addressingDecision)
	shouldLaunch := addressingDecision.ShouldRespond || ambientDuty.IsMatch
	if !shouldLaunch && addressingDecision.ReactionEmoji == "" {
		return inboundEngagementDecision{IgnoreReason: "addressing_" + string(addressingDecision.Target) + " dutyMatch=false"}
	}
	return inboundEngagementDecision{
		ShouldLaunch:  shouldLaunch,
		SuppressReply: AmbientDutyLaunchesWithoutReply(addressingDecision),
		ReactionEmoji: addressingDecision.ReactionEmoji,
		AmbientDuty:   ambientDuty,
	}
}

func isMultiPersonConversation(event PlatformInboundEvent) bool {
	conversationType := strings.ToLower(strings.TrimSpace(event.Context.ConversationType))
	if conversationType == "" {
		return false
	}
	switch conversationType {
	case "d", "dm", "im", "direct":
		return false
	}
	return true
}

func AmbientDutyLaunchesWithoutReply(decision agentcontract.AddressingDecision) bool {
	return !decision.ShouldRespond && ambientDutyContextFromAddressingDecision(decision).IsMatch
}

func ambientDutyContextFromAddressingDecision(decision agentcontract.AddressingDecision) agentcontract.AmbientDutyContext {
	if !decision.DutyMatch || decision.DutyConfidence < ambientDutyLaunchConfidenceThreshold {
		return agentcontract.AmbientDutyContext{}
	}
	return (agentcontract.AmbientDutyContext{
		IsMatch:    true,
		Name:       decision.DutyName,
		Confidence: decision.DutyConfidence,
	}).Normalized()
}
