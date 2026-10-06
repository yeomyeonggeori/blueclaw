package connectors

import (
	"context"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const connectorDecisionBurstSize = 4

const connectorDecisionBurstWindow = 30 * time.Second

func (connectorRuntime *ConnectorRuntime) decideClaimedBurst(ctx context.Context, queuedEvents []QueuedConnectorEvent) {
	for index := range queuedEvents {
		queuedEvents[index].Event = withGatewayDecision(queuedEvents[index].Event)
	}
	for _, burst := range inboundDecisionBursts(connectorRuntime.eventsNeedingTheGateway(queuedEvents)) {
		connectorRuntime.decideInboundBurst(ctx, burst)
	}
}

func (connectorRuntime *ConnectorRuntime) eventsNeedingTheGateway(queuedEvents []QueuedConnectorEvent) []QueuedConnectorEvent {
	needing := []QueuedConnectorEvent{}
	for _, queuedEvent := range queuedEvents {
		if !connectorRuntime.isDecidedBeforeTheGateway(queuedEvent.Event) {
			needing = append(needing, queuedEvent)
		}
	}
	return needing
}

func (connectorRuntime *ConnectorRuntime) isDecidedBeforeTheGateway(event PlatformInboundEvent) bool {
	if exactTaskControlIntent(event.Prompt) != agentcontract.TaskControlIntentNone {
		return true
	}
	return connectorRuntime.isReplyToAskingThread(event)
}

func (connectorRuntime *ConnectorRuntime) decideInboundBurst(ctx context.Context, events []PlatformInboundEvent) {
	adapter, errorValue := connectorRuntime.findAdapter(events[0].Platform)
	if errorValue != nil {
		return
	}
	facts := connectorRuntime.inboundGatewayFacts(ctx, adapter, events[len(events)-1])
	if isDirectMessageWithNothingOpen(facts) {
		return
	}
	for _, fittingEvents := range connectorRuntime.burstsWithinTheRequestBudget(facts, events) {
		if len(fittingEvents) < 2 {
			continue
		}
		connectorRuntime.decideFittingBurst(ctx, facts, fittingEvents)
	}
}

func (connectorRuntime *ConnectorRuntime) decideFittingBurst(ctx context.Context, facts inboundengagement.Facts, events []PlatformInboundEvent) {
	facts.Messages = burstDecisionMessages(events)
	judgments, errorValue := connectorRuntime.judgeFacts(ctx, facts)
	for _, event := range events {
		seedInboundDecision(event, judgments, errorValue)
	}
}

func (connectorRuntime *ConnectorRuntime) burstsWithinTheRequestBudget(facts inboundengagement.Facts, events []PlatformInboundEvent) [][]PlatformInboundEvent {
	bursts := [][]PlatformInboundEvent{}
	burst := []PlatformInboundEvent{}
	for _, event := range events {
		candidateBurst := append(append([]PlatformInboundEvent{}, burst...), event)
		if len(burst) > 0 && !connectorRuntime.decisionRequestFitsTheBudget(facts, candidateBurst) {
			bursts = append(bursts, burst)
			burst = []PlatformInboundEvent{event}
			continue
		}
		burst = candidateBurst
	}
	return append(bursts, burst)
}

func (connectorRuntime *ConnectorRuntime) decisionRequestFitsTheBudget(facts inboundengagement.Facts, events []PlatformInboundEvent) bool {
	facts.Messages = burstDecisionMessages(events)
	return connectorRuntime.gatewayDecider == nil || connectorRuntime.gatewayDecider.FitsBurstBudget(facts)
}

func burstDecisionMessages(events []PlatformInboundEvent) []inboundengagement.Message {
	messages := []inboundengagement.Message{}
	for _, event := range events {
		messages = append(messages, inboundDecisionMessage(event))
	}
	return messages
}

func seedInboundDecision(event PlatformInboundEvent, judgments []inboundengagement.Judgment, burstError error) {
	if event.gatewayDecision == nil {
		return
	}
	event.gatewayDecision.once.Do(func() {
		if burstError != nil {
			event.gatewayDecision.errorValue = burstError
			return
		}
		event.gatewayDecision.judgment, event.gatewayDecision.errorValue = judgmentForMessage(judgments, event.MessageID)
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
	return !isIgnoredWithoutDeciding(event)
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

func (connectorRuntime *ConnectorRuntime) isReplyToAskingThread(event PlatformInboundEvent) bool {
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(event.Platform, event.SenderID)
	if !isFound {
		return false
	}
	for _, thread := range connectorRuntime.askingThreads.awaiting(event.Platform, event.ConversationID, personID) {
		if connectorRuntime.postedQuestionOf(thread).IsAnsweredBy(placementOf(event)) {
			return true
		}
	}
	return false
}

func isDirectMessageWithNothingOpen(facts inboundengagement.Facts) bool {
	return !inboundengagement.IsMultiPersonConversation(facts.ConversationType) && facts.OpenTask == nil && facts.FinishedTask == nil
}
