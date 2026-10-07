package connectors

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func (connectorRuntime *ConnectorRuntime) UseGatewayDecider(gatewayDecider inboundengagement.Decider) {
	connectorRuntime.gatewayDecider = gatewayDecider
}

func (connectorRuntime *ConnectorRuntime) UseTasklessLLMCallRecorder(recordTasklessLLMCall func(subjects []string, record agentcontract.LLMCallRecord)) {
	connectorRuntime.recordTasklessLLMCall = recordTasklessLLMCall
}

type gatewayDecision struct {
	once       sync.Once
	judgment   inboundengagement.Judgment
	errorValue error
}

func withGatewayDecision(event PlatformInboundEvent) PlatformInboundEvent {
	if event.gatewayDecision != nil {
		return event
	}
	event.gatewayDecision = &gatewayDecision{}
	return event
}

func (connectorRuntime *ConnectorRuntime) judgeInboundMessage(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (inboundengagement.Judgment, error) {
	return connectorRuntime.judgeEvent(ctx, event, func() *inboundTurn { return connectorRuntime.inboundTurnFor(adapter, event) })
}

func (connectorRuntime *ConnectorRuntime) judgeTurn(ctx context.Context, turn *inboundTurn) (inboundengagement.Judgment, error) {
	return connectorRuntime.judgeEvent(ctx, turn.event, func() *inboundTurn { return turn })
}

func (connectorRuntime *ConnectorRuntime) judgeEvent(ctx context.Context, event PlatformInboundEvent, newTurn func() *inboundTurn) (inboundengagement.Judgment, error) {
	if event.gatewayDecision == nil {
		return connectorRuntime.judgeNow(ctx, newTurn())
	}
	event.gatewayDecision.once.Do(func() {
		event.gatewayDecision.judgment, event.gatewayDecision.errorValue = connectorRuntime.judgeNow(ctx, newTurn())
	})
	return event.gatewayDecision.judgment, event.gatewayDecision.errorValue
}

func (connectorRuntime *ConnectorRuntime) judgeNow(ctx context.Context, turn *inboundTurn) (inboundengagement.Judgment, error) {
	facts := connectorRuntime.gatewayFactsForTurn(ctx, turn)
	judgments, errorValue := connectorRuntime.judgeFacts(ctx, facts)
	if errorValue != nil {
		return inboundengagement.Judgment{}, errorValue
	}
	return judgmentForMessage(judgments, turn.event.MessageID)
}

func judgmentForMessage(judgments []inboundengagement.Judgment, messageID string) (inboundengagement.Judgment, error) {
	trimmedMessageID := strings.TrimSpace(messageID)
	for _, judgment := range judgments {
		if judgment.MessageID == trimmedMessageID {
			return judgment, nil
		}
	}
	return inboundengagement.Judgment{}, errors.New("the gateway decision answered about no message " + messageID)
}

func (connectorRuntime *ConnectorRuntime) judgeFacts(ctx context.Context, facts inboundengagement.Facts) ([]inboundengagement.Judgment, error) {
	if connectorRuntime.gatewayDecider == nil {
		return nil, errors.New("connector runtime has no gateway decider configured")
	}
	callRecords := []agentcontract.LLMCallRecord{}
	judgments, errorValue := connectorRuntime.gatewayDecider.Decide(ctx, facts, func(record agentcontract.LLMCallRecord) { callRecords = append(callRecords, record) })
	connectorRuntime.recordTasklessGatewayCalls(facts.Messages, callRecords)
	return judgments, errorValue
}

func (connectorRuntime *ConnectorRuntime) recordTasklessGatewayCalls(messages []inboundengagement.Message, callRecords []agentcontract.LLMCallRecord) {
	if connectorRuntime.recordTasklessLLMCall == nil {
		return
	}
	for _, callRecord := range callRecords {
		connectorRuntime.recordTasklessLLMCall(JudgedMessageIDs(callRecord, messages), callRecord)
	}
}

func JudgedMessageIDs(callRecord agentcontract.LLMCallRecord, messages []inboundengagement.Message) []string {
	if len(callRecord.DecidedMessageIDs) > 0 {
		return callRecord.DecidedMessageIDs
	}
	messageIDs := make([]string, 0, len(messages))
	for _, message := range messages {
		messageIDs = append(messageIDs, message.MessageID)
	}
	return messageIDs
}

func heldGatewayDecisionAttributes(event PlatformInboundEvent) []any {
	if event.gatewayDecision == nil || strings.TrimSpace(event.gatewayDecision.judgment.MessageID) == "" {
		return nil
	}
	judgment := event.gatewayDecision.judgment
	return []any{
		slog.String("target", string(judgment.Addressing.Target)),
		slog.Bool("shouldRespond", judgment.Addressing.ShouldRespond),
		slog.Float64("reactionProbability", judgment.ReactionProbability),
	}
}

func (connectorRuntime *ConnectorRuntime) recordCallsOnTask(taskRunID string, callRecords []agentcontract.LLMCallRecord) {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if trimmedTaskRunID == "" || connectorRuntime.taskRunService == nil {
		return
	}
	for _, callRecord := range callRecords {
		connectorRuntime.taskRunService.AppendLLMCall(trimmedTaskRunID, callRecord)
	}
}

func (connectorRuntime *ConnectorRuntime) inboundTurnFor(adapter PlatformAdapter, event PlatformInboundEvent) *inboundTurn {
	personID, _ := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderID)
	return &inboundTurn{
		adapter:        adapter,
		platform:       event.Platform,
		event:          event,
		personID:       personID,
		personAccess:   connectorRuntime.identityService.ResolvePersonAccess(personID),
		requesterEmail: connectorRuntime.requesterEmailForEvent(personID, event),
	}
}

func (connectorRuntime *ConnectorRuntime) inboundGatewayFacts(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) inboundengagement.Facts {
	return connectorRuntime.gatewayFactsForTurn(ctx, connectorRuntime.inboundTurnFor(adapter, event))
}

func (connectorRuntime *ConnectorRuntime) gatewayFactsForTurn(ctx context.Context, turn *inboundTurn) inboundengagement.Facts {
	if turn.adapter != nil {
		turn.event = connectorRuntime.withInitialVisibleContext(ctx, turn.adapter, turn.event)
	}
	open := connectorRuntime.readOpenInteractions(turn)
	facts := inboundengagement.Facts{
		Messages:         []inboundengagement.Message{inboundDecisionMessage(turn.event)},
		ConversationType: turn.event.Context.ConversationType,
		VisibleContext:   turn.event.Context.ToAgentVisibleContext(),
		AgentIdentity:    connectorRuntime.agentIdentity(),
		Company:          connectorRuntime.company(),
		OpenTask:         connectorRuntime.openTaskFacts(open),
		Duties:           inboundengagement.StandingDuties(),
		EnvironmentNow:   time.Now(),
	}
	if facts.OpenTask == nil {
		facts.FinishedTask = connectorRuntime.finishedTaskFacts(turn.personID, turn.event)
	}
	return facts
}

func (connectorRuntime *ConnectorRuntime) openTaskFacts(open openInteractions) *inboundengagement.TaskFacts {
	if open.hasAsk {
		taskFacts := connectorRuntime.taskFactsOf(open.askTaskRun)
		taskFacts.PostedQuestion = open.ask.Question
		return &taskFacts
	}
	if open.hasRunningTask {
		taskFacts := connectorRuntime.taskFactsOf(open.runningTask)
		return &taskFacts
	}
	return nil
}

func (connectorRuntime *ConnectorRuntime) finishedTaskFacts(personID string, event PlatformInboundEvent) *inboundengagement.TaskFacts {
	finishedTaskRun, isFound := connectorRuntime.latestRecentlyFinishedConversationTask(personID, event)
	if !isFound {
		return nil
	}
	taskFacts := connectorRuntime.taskFactsOf(finishedTaskRun)
	return &taskFacts
}

func inboundDecisionMessage(event PlatformInboundEvent) inboundengagement.Message {
	return inboundengagement.Message{
		MessageID:         event.MessageID,
		Prompt:            event.Prompt,
		SenderName:        event.Context.Sender.Name,
		SenderHandle:      event.Context.Sender.Handle,
		BotMentioned:      event.Context.Addressing.BotMentioned,
		SentAt:            event.RawReceivedAt,
		InputParts:        event.InputParts,
		Attachments:       agentcontract.AttachmentFactsFromParts(event.InputParts),
		IsAttachmentsOnly: event.Context.AttachmentsOnly,
	}
}

func (connectorRuntime *ConnectorRuntime) relatesToActiveTask(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) bool {
	judgment, errorValue := connectorRuntime.judgeInboundMessage(ctx, adapter, event)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".gateway.decision_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return false
	}
	return judgment.HasRelatesToActiveTask && judgment.RelatesToActiveTask
}
