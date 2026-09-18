package connectors

import (
	"context"
	"errors"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

// A person says one thing in three messages. When they are all waiting when the
// worker claims, they are one arrival, and one decision call answers about all
// of them.
const connectorDecisionBurstSize = 4

// A decision call carries one question set per message, so the messages' own
// text is the part that grows without bound. Past this the burst is cut and the
// rest are decided in the next call.
const connectorDecisionBurstPromptBudgetBytes = 8000

// Messages further apart than this are a backlog rather than a burst, and a
// backlog's later messages deserve the state the earlier ones left behind.
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
	decisionRequest.Messages = burstDecisionMessages(events)
	decisions, callRecords, errorValue := connectorRuntime.decideBurst(ctx, decisionRequest, ledgerTaskRunID)
	for _, event := range events {
		seedInboundDecision(event, decisions, errorValue)
		holdIntakeCallRecords(event.intakeDecision, ledgerTaskRunID, callRecords)
	}
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

// isBurstDecidableEvent keeps a burst to what one shared state describes: one
// conversation, one sender, one message each.
func isBurstDecidableEvent(event PlatformInboundEvent) bool {
	return event.TaskRetry == nil && event.MessageID != "" && event.SenderID != "" && event.ConversationID != ""
}

func inboundDecisionBurstKey(event PlatformInboundEvent) string {
	return event.Platform + ":" + event.ConversationID + ":" + event.SenderID
}

func burstsWithinBudget(events []PlatformInboundEvent) [][]PlatformInboundEvent {
	bursts := [][]PlatformInboundEvent{}
	burst := []PlatformInboundEvent{}
	promptByteCount := 0
	for _, event := range events {
		if len(burst) > 0 && !burstAccepts(burst, promptByteCount, event) {
			bursts = append(bursts, burst)
			burst = []PlatformInboundEvent{}
			promptByteCount = 0
		}
		burst = append(burst, event)
		promptByteCount += len(event.Prompt)
	}
	bursts = append(bursts, burst)
	return burstsOfSeveralMessages(bursts)
}

func burstAccepts(burst []PlatformInboundEvent, promptByteCount int, event PlatformInboundEvent) bool {
	if len(burst) >= connectorDecisionBurstSize {
		return false
	}
	if promptByteCount+len(event.Prompt) > connectorDecisionBurstPromptBudgetBytes {
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

// burstsOfSeveralMessages drops the bursts of one, because a lone message is
// already decided once, by whichever consumer asks about it first.
func burstsOfSeveralMessages(bursts [][]PlatformInboundEvent) [][]PlatformInboundEvent {
	burstsWorthBatching := [][]PlatformInboundEvent{}
	for _, burst := range bursts {
		if len(burst) > 1 {
			burstsWorthBatching = append(burstsWorthBatching, burst)
		}
	}
	return burstsWorthBatching
}
