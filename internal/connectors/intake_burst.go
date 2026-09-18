package connectors

import (
	"context"
	"errors"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
)

const connectorDecisionBurstSize = 4

const connectorDecisionRequestByteCeiling = 80000

const connectorDecisionBurstWindow = 30 * time.Second

func (connectorRuntime *ConnectorRuntime) decideClaimedBurst(ctx context.Context, queuedEvents []QueuedConnectorEvent) {
	for index := range queuedEvents {
		queuedEvents[index].Event = withInboundDecision(queuedEvents[index].Event)
	}
	for _, burst := range inboundDecisionBursts(queuedEvents) {
		connectorRuntime.decideInboundBurst(ctx, burst)
	}
}

func (connectorRuntime *ConnectorRuntime) decideInboundBurst(ctx context.Context, events []PlatformInboundEvent) {
	adapter, errorValue := connectorRuntime.findAdapter(events[0].Platform)
	if errorValue != nil {
		return
	}
	decisionRequest, ledgerTaskRunID := connectorRuntime.inboundDecisionRequest(ctx, adapter, events[len(events)-1])
	for _, fittingEvents := range burstsWithinTheRequestCeiling(decisionRequest, events) {
		if len(fittingEvents) < 2 {
			continue
		}
		connectorRuntime.decideFittingBurst(ctx, decisionRequest, ledgerTaskRunID, fittingEvents)
	}
}

func (connectorRuntime *ConnectorRuntime) decideFittingBurst(ctx context.Context, decisionRequest agentcontract.IntakeDecisionRequest, ledgerTaskRunID string, events []PlatformInboundEvent) {
	decisionRequest.Messages = burstDecisionMessages(events)
	decisions, callRecords, errorValue := connectorRuntime.decideBurst(ctx, decisionRequest, ledgerTaskRunID)
	for _, event := range events {
		seedInboundDecision(event, decisions, errorValue)
	}
	holdBurstIntakeCallRecords(events, ledgerTaskRunID, callRecords)
}

func burstsWithinTheRequestCeiling(decisionRequest agentcontract.IntakeDecisionRequest, events []PlatformInboundEvent) [][]PlatformInboundEvent {
	bursts := [][]PlatformInboundEvent{}
	burst := []PlatformInboundEvent{}
	for _, event := range events {
		candidateBurst := append(append([]PlatformInboundEvent{}, burst...), event)
		if len(burst) > 0 && !decisionRequestFitsTheCeiling(decisionRequest, candidateBurst) {
			bursts = append(bursts, burst)
			burst = []PlatformInboundEvent{event}
			continue
		}
		burst = candidateBurst
	}
	return append(bursts, burst)
}

func decisionRequestFitsTheCeiling(decisionRequest agentcontract.IntakeDecisionRequest, events []PlatformInboundEvent) bool {
	decisionRequest.Messages = burstDecisionMessages(events)
	return intake.DecisionRequestByteCount(decisionRequest) <= connectorDecisionRequestByteCeiling
}

func (connectorRuntime *ConnectorRuntime) decideBurst(ctx context.Context, decisionRequest agentcontract.IntakeDecisionRequest, ledgerTaskRunID string) (agentcontract.IntakeDecisions, []agentcontract.LLMCallRecord, error) {
	if connectorRuntime.intakeDecider == nil {
		return agentcontract.IntakeDecisions{}, nil, errors.New("connector runtime has no intake decider configured")
	}
	callLedger := &agentcontract.IntakeCallLedger{}
	decisions, errorValue := connectorRuntime.intakeDecider.Decide(ctx, decisionRequest, callLedger)
	connectorRuntime.recordIntakeCalls(ledgerTaskRunID, callLedger.Records)
	return decisions, callLedger.Records, errorValue
}

func burstDecisionMessages(events []PlatformInboundEvent) []agentcontract.IntakeDecisionMessage {
	messages := []agentcontract.IntakeDecisionMessage{}
	for _, event := range events {
		messages = append(messages, inboundDecisionMessage(event))
	}
	return messages
}

func seedInboundDecision(event PlatformInboundEvent, decisions agentcontract.IntakeDecisions, burstError error) {
	if event.intakeDecision == nil {
		return
	}
	event.intakeDecision.once.Do(func() {
		if burstError != nil {
			event.intakeDecision.errorValue = burstError
			return
		}
		decision, isDecided := decisions.ForMessage(event.MessageID)
		if !isDecided {
			event.intakeDecision.errorValue = errors.New("the intake decision answered about no message " + event.MessageID)
			return
		}
		event.intakeDecision.decision = decision
	})
}

func inboundDecisionBursts(queuedEvents []QueuedConnectorEvent) [][]PlatformInboundEvent {
	eventsByConversation := map[string][]PlatformInboundEvent{}
	conversationOrder := []string{}
	for _, queuedEvent := range queuedEvents {
		if !isBurstDecidableEvent(queuedEvent.Event) {
			continue
		}
		conversationKey := inboundDecisionBurstKey(queuedEvent.Event)
		if _, isSeen := eventsByConversation[conversationKey]; !isSeen {
			conversationOrder = append(conversationOrder, conversationKey)
		}
		eventsByConversation[conversationKey] = append(eventsByConversation[conversationKey], queuedEvent.Event)
	}
	bursts := [][]PlatformInboundEvent{}
	for _, conversationKey := range conversationOrder {
		bursts = append(bursts, burstsWithinBudget(eventsByConversation[conversationKey])...)
	}
	return bursts
}

func isBurstDecidableEvent(event PlatformInboundEvent) bool {
	if event.TaskRetry != nil || event.MessageID == "" || event.SenderID == "" || event.ConversationID == "" {
		return false
	}
	return !inboundengagement.IsIgnoredWithoutDeciding(engagementRequestForEvent(event))
}

func inboundDecisionBurstKey(event PlatformInboundEvent) string {
	return event.Platform + ":" + event.ConversationID + ":" + event.SenderID
}

func burstsWithinBudget(events []PlatformInboundEvent) [][]PlatformInboundEvent {
	bursts := [][]PlatformInboundEvent{}
	burst := []PlatformInboundEvent{}
	for _, event := range events {
		if len(burst) > 0 && !burstAccepts(burst, event) {
			bursts = append(bursts, burst)
			burst = []PlatformInboundEvent{}
		}
		burst = append(burst, event)
	}
	bursts = append(bursts, burst)
	return burstsOfSeveralMessages(bursts)
}

func burstAccepts(burst []PlatformInboundEvent, event PlatformInboundEvent) bool {
	if len(burst) >= connectorDecisionBurstSize {
		return false
	}
	return isWithinBurstWindow(burst[0].RawReceivedAt, event.RawReceivedAt)
}

func isWithinBurstWindow(firstReceivedAt time.Time, receivedAt time.Time) bool {
	if firstReceivedAt.IsZero() || receivedAt.IsZero() {
		return true
	}
	return receivedAt.Sub(firstReceivedAt) <= connectorDecisionBurstWindow
}

func burstsOfSeveralMessages(bursts [][]PlatformInboundEvent) [][]PlatformInboundEvent {
	burstsWorthBatching := [][]PlatformInboundEvent{}
	for _, burst := range bursts {
		if len(burst) > 1 {
			burstsWorthBatching = append(burstsWorthBatching, burst)
		}
	}
	return burstsWorthBatching
}
