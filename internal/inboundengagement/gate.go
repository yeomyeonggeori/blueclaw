package inboundengagement

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const ambientDutyLaunchConfidenceThreshold = 0.7

type AddressingClassifier interface {
	ClassifyAddressing(context.Context, agentcontract.AddressingClassificationRequest) (agentcontract.AddressingDecision, error)
}

type Decision struct {
	ShouldLaunch  bool
	SuppressReply bool
	ReactionEmoji string
	IgnoreReason  string
	AmbientDuty   agentcontract.AmbientDutyContext
}

type Request struct {
	Prompt           string
	MessageID        string
	ConversationType string
	BotMentioned     bool
	AttachmentsOnly  bool
	MessageSentAt    time.Time
	SenderName       string
	SenderHandle     string
	VisibleContext   agentcontract.VisibleContext
}

type Gate struct {
	addressingClassifier  AddressingClassifier
	agentIdentityProvider func() agentcontract.AgentIdentity
	companyProvider       func() agentcontract.CompanyContext
	logger                *slog.Logger
}

func NewGate(addressingClassifier AddressingClassifier, agentIdentityProvider func() agentcontract.AgentIdentity, companyProvider func() agentcontract.CompanyContext, logger *slog.Logger) *Gate {
	if logger == nil {
		logger = slog.Default()
	}
	return &Gate{
		addressingClassifier:  addressingClassifier,
		agentIdentityProvider: agentIdentityProvider,
		companyProvider:       companyProvider,
		logger:                logger,
	}
}

func (gate *Gate) Resolve(ctx context.Context, platform string, request Request) Decision {
	if !IsMultiPersonConversation(request.ConversationType) {
		return Decision{ShouldLaunch: true}
	}
	if request.AttachmentsOnly && !request.BotMentioned {
		return Decision{IgnoreReason: "attachments_only_uninvited"}
	}
	addressingDecision, errorValue := gate.addressingClassifier.ClassifyAddressing(ctx, agentcontract.AddressingClassificationRequest{
		Prompt:           request.Prompt,
		AgentIdentity:    gate.agentIdentity(),
		Company:          gate.company(),
		BotMentioned:     request.BotMentioned,
		MessageSentAt:    request.MessageSentAt,
		ConversationType: request.ConversationType,
		SenderName:       request.SenderName,
		SenderHandle:     request.SenderHandle,
		VisibleContext:   request.VisibleContext,
	})
	if errorValue != nil {
		gate.logger.Warn("connector."+platform+".addressing.classifier_failed", slog.String("messageID", request.MessageID), slog.String("error", errorValue.Error()))
		if request.BotMentioned {
			return Decision{ShouldLaunch: true}
		}
		return Decision{IgnoreReason: "addressing_classifier_failed dutyMatch=false"}
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

func (gate *Gate) agentIdentity() agentcontract.AgentIdentity {
	if gate.agentIdentityProvider == nil {
		return agentcontract.AgentIdentity{}
	}
	return gate.agentIdentityProvider()
}

func (gate *Gate) company() agentcontract.CompanyContext {
	if gate.companyProvider == nil {
		return agentcontract.CompanyContext{}
	}
	return gate.companyProvider()
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
