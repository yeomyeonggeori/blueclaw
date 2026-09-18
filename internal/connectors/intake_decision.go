package connectors

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

// IntakeDecider answers every closed question about an inbound message in one
// call: who it is addressed to, whether it follows on from the task already
// running, and every field the turn router used to decide in prose.
type IntakeDecider interface {
	Decide(context.Context, agentcontract.IntakeDecisionRequest, *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error)
}

func (connectorRuntime *ConnectorRuntime) UseIntakeDecider(intakeDecider IntakeDecider) {
	connectorRuntime.intakeDecider = intakeDecider
}

// inboundDecision holds the one decision made about one message. The gate, the
// follow-up fast path and the turn router all read it, and whichever asks first
// pays for it.
type inboundDecision struct {
	once       sync.Once
	decision   agentcontract.IntakeMessageDecision
	errorValue error
}

func withInboundDecision(event PlatformInboundEvent) PlatformInboundEvent {
	if event.intakeDecision != nil {
		return event
	}
	event.intakeDecision = &inboundDecision{}
	return event
}

func (connectorRuntime *ConnectorRuntime) decideInboundMessage(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (agentcontract.IntakeMessageDecision, error) {
	if event.intakeDecision == nil {
		return connectorRuntime.decideInboundMessageNow(ctx, adapter, event)
	}
	event.intakeDecision.once.Do(func() {
		event.intakeDecision.decision, event.intakeDecision.errorValue = connectorRuntime.decideInboundMessageNow(ctx, adapter, event)
	})
	return event.intakeDecision.decision, event.intakeDecision.errorValue
}

func (connectorRuntime *ConnectorRuntime) decideInboundMessageNow(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (agentcontract.IntakeMessageDecision, error) {
	if connectorRuntime.intakeDecider == nil {
		return agentcontract.IntakeMessageDecision{}, errors.New("connector runtime has no intake decider configured")
	}
	decisionRequest, ledgerTaskRunID := connectorRuntime.inboundDecisionRequest(ctx, adapter, event)
	callLedger := &agentcontract.IntakeCallLedger{}
	decisions, errorValue := connectorRuntime.intakeDecider.Decide(ctx, decisionRequest, callLedger)
	connectorRuntime.recordIntakeCalls(ledgerTaskRunID, callLedger.Records)
	if errorValue != nil {
		return agentcontract.IntakeMessageDecision{}, errorValue
	}
	decision, isDecided := decisions.ForMessage(event.MessageID)
	if !isDecided {
		return agentcontract.IntakeMessageDecision{}, errors.New("the intake decision answered about no message " + event.MessageID)
	}
	return decision, nil
}

func (connectorRuntime *ConnectorRuntime) recordIntakeCalls(taskRunID string, callRecords []agentcontract.LLMCallRecord) {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if trimmedTaskRunID == "" || connectorRuntime.taskRunService == nil {
		return
	}
	for _, callRecord := range callRecords {
		connectorRuntime.taskRunService.AppendTaskEvent(trimmedTaskRunID, agentcontract.TaskEventLLMCall, marshalConnectorEventBody(callRecord))
	}
}

// inboundDecisionRequest assembles the whole state the decision reads. It is
// built where the message is claimed rather than inside the turn, because the
// fast path that skips the conversation lock needs the same answer the gate and
// the router need.
func (connectorRuntime *ConnectorRuntime) inboundDecisionRequest(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (agentcontract.IntakeDecisionRequest, string) {
	personID, _ := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderID)
	turn := &inboundTurn{
		adapter:        adapter,
		platform:       event.Platform,
		event:          connectorRuntime.withInitialVisibleContext(ctx, adapter, event),
		personID:       personID,
		personAccess:   connectorRuntime.identityService.ResolvePersonAccess(personID),
		requesterEmail: connectorRuntime.requesterEmailForEvent(personID, event),
	}
	turn.taskWaitResolution = connectorRuntime.resolveInboundTaskWait(personID, turn.platform, turn.event)
	open := connectorRuntime.readOpenInteractions(turn)
	priorTask, _ := connectorRuntime.findPriorTaskContext(personID, turn.event)
	decisionRequest := agentcontract.IntakeDecisionRequest{
		Messages:         []agentcontract.IntakeDecisionMessage{inboundDecisionMessage(turn.event)},
		ConversationType: turn.event.Context.ConversationType,
		VisibleContext:   turn.event.Context.ToAgentVisibleContext(),
		AgentIdentity:    connectorRuntime.agentIdentity(),
		Company:          connectorRuntime.company(),
		PriorTask:        priorTask,
		ToolSet:          connectorRuntime.routerToolSetForTurn(turn),
		ResponseLanguage: responseLanguageForEvent(turn.event),
		EnvironmentNow:   time.Now(),
	}
	if open.hasConfirmation {
		decisionRequest.PendingConfirmation = agentcontract.PendingConfirmationContext{
			TaskRunID:      open.confirmation.TaskRun.TaskRunID,
			Prompt:         open.confirmation.IntentPrompt,
			Question:       open.confirmation.ApprovalQuestion,
			AskedAt:        open.confirmationAt,
			ExchangesSince: connectorRuntime.exchangesSince(turn, open.confirmationAt, open.confirmation.TaskRun.TaskRunID),
		}
	}
	if open.hasAsk {
		decisionRequest.PendingChoice = agentcontract.PendingChoiceContext{
			TaskRunID:      open.ask.TaskRunID,
			Question:       open.ask.Question,
			SelectionMode:  open.ask.SelectionMode,
			Options:        choiceReplyOptions(open.ask.Options),
			AskedAt:        open.askAt,
			ExchangesSince: connectorRuntime.exchangesSince(turn, open.askAt, open.ask.TaskRunID),
		}
	}
	if open.hasRunningTask {
		decisionRequest.ActiveTask = connectorRuntime.activeTaskContext(open.runningTask)
	}
	if finishedTaskRun, isFound := connectorRuntime.latestRecentlyFinishedConversationTask(personID, turn.event); isFound && decisionRequest.ActiveTask.TaskRunID == "" {
		decisionRequest.ActiveTask = connectorRuntime.activeTaskContext(finishedTaskRun)
		decisionRequest.IsTaskRecentlyFinished = true
	}
	return decisionRequest, open.ledgerTaskRunID()
}

func inboundDecisionMessage(event PlatformInboundEvent) agentcontract.IntakeDecisionMessage {
	return agentcontract.IntakeDecisionMessage{
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

// eventAddressingDecider hands the gate the addressing half of the one decision
// made about this message, so the gate reads rather than asks.
type eventAddressingDecider struct {
	connectorRuntime *ConnectorRuntime
	adapter          PlatformAdapter
	event            PlatformInboundEvent
}

func (decider eventAddressingDecider) DecideAddressing(ctx context.Context, _ inboundengagement.Request) (agentcontract.AddressingDecision, error) {
	decision, errorValue := decider.connectorRuntime.decideInboundMessage(ctx, decider.adapter, decider.event)
	return decision.Addressing, errorValue
}

// DecideAddressing answers for a caller that has a message but no queued
// inbound event, such as an editor session over ACP.
func (connectorRuntime *ConnectorRuntime) DecideAddressing(ctx context.Context, request inboundengagement.Request) (agentcontract.AddressingDecision, error) {
	if connectorRuntime.intakeDecider == nil {
		return agentcontract.AddressingDecision{}, errors.New("connector runtime has no intake decider configured")
	}
	decisions, errorValue := connectorRuntime.intakeDecider.Decide(ctx, agentcontract.IntakeDecisionRequest{
		Messages: []agentcontract.IntakeDecisionMessage{{
			MessageID:    request.MessageID,
			Prompt:       request.Prompt,
			SenderName:   request.SenderName,
			SenderHandle: request.SenderHandle,
			BotMentioned: request.BotMentioned,
			SentAt:       request.MessageSentAt,
		}},
		ConversationType: request.ConversationType,
		VisibleContext:   request.VisibleContext,
		AgentIdentity:    connectorRuntime.agentIdentity(),
		Company:          connectorRuntime.company(),
		EnvironmentNow:   time.Now(),
	}, nil)
	if errorValue != nil {
		return agentcontract.AddressingDecision{}, errorValue
	}
	if len(decisions.Messages) == 0 {
		return agentcontract.AddressingDecision{}, errors.New("the intake decision answered about no message")
	}
	return decisions.Messages[0].Addressing, nil
}

func (connectorRuntime *ConnectorRuntime) relatesToActiveTask(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) bool {
	decision, errorValue := connectorRuntime.decideInboundMessage(ctx, adapter, event)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".intake.decision_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return false
	}
	return decision.RelatesToActiveTask
}

func (connectorRuntime *ConnectorRuntime) decidedTurnFields(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) *agentcontract.TurnDecision {
	decision, errorValue := connectorRuntime.decideInboundMessage(ctx, adapter, event)
	if errorValue != nil {
		return nil
	}
	turnFields := decision.TurnFields
	return &turnFields
}
