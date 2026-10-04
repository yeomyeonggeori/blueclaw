package acpsession

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type IntakeDecider interface {
	Decide(context.Context, agentcontract.IntakeDecisionRequest, *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error)
}

type messageDecision struct {
	intakeDecider  IntakeDecider
	decisionInput  func(context.Context) agentcontract.IntakeDecisionRequest
	once           sync.Once
	decision       agentcontract.IntakeMessageDecision
	callRecords    []agentcontract.LLMCallRecord
	errorValue     error
	wasEverDecided bool
}

func (agent *Agent) newMessageDecision(sessionTurn *connectors.SessionTurn) *messageDecision {
	decisionInput := func(ctx context.Context) agentcontract.IntakeDecisionRequest {
		return sessionTurn.DecisionRequest(ctx)
	}
	return &messageDecision{intakeDecider: agent.intakeDecider, decisionInput: decisionInput}
}

func (memo *messageDecision) DecideAddressing(ctx context.Context, _ inboundengagement.Request) (agentcontract.AddressingDecision, error) {
	decision, errorValue := memo.decide(ctx)
	return decision.Addressing, errorValue
}

func (memo *messageDecision) decide(ctx context.Context) (agentcontract.IntakeMessageDecision, error) {
	memo.once.Do(func() {
		memo.wasEverDecided = true
		callLedger := &agentcontract.IntakeCallLedger{}
		decisions, errorValue := memo.intakeDecider.Decide(ctx, memo.decisionInput(ctx), callLedger)
		memo.callRecords = callLedger.Records
		memo.decision, memo.errorValue = firstMessageDecision(decisions, errorValue)
	})
	return memo.decision, memo.errorValue
}

func firstMessageDecision(decisions agentcontract.IntakeDecisions, errorValue error) (agentcontract.IntakeMessageDecision, error) {
	if errorValue != nil {
		return agentcontract.IntakeMessageDecision{}, errorValue
	}
	if len(decisions.Messages) == 0 {
		return agentcontract.IntakeMessageDecision{}, errors.New("the intake decision answered about no message")
	}
	return decisions.Messages[0], nil
}

func (memo *messageDecision) decidedTurnFields() *agentcontract.TurnDecision {
	if !memo.wasEverDecided || memo.errorValue != nil {
		return nil
	}
	turnFields := memo.decision.TurnFields
	return &turnFields
}

func (agent *Agent) decideOnce(ctx context.Context, session openSession, messageContext MessageContext, launchRequest agentruntime.TaskLaunchRequest, sessionTurn *connectors.SessionTurn) (agentruntime.TaskLaunchRequest, *messageDecision, string) {
	if agent.intakeDecider == nil {
		return launchRequest, nil, ""
	}
	memo := agent.newMessageDecision(sessionTurn)
	gate := inboundengagement.NewGate(memo, agent.logger)
	engagement := gate.Resolve(ctx, session.context.Addressing.Platform, inboundengagement.Request{
		Prompt:           launchRequest.Prompt,
		MessageID:        messageContext.MessageID,
		ConversationType: launchRequest.ConversationType,
		BotMentioned:     messageContext.Context.Addressing.BotMentioned,
		AttachmentsOnly:  messageContext.Context.AttachmentsOnly,
		SenderName:       messageContext.Context.Sender.Name,
		SenderHandle:     messageContext.Context.Sender.Handle,
		VisibleContext:   launchRequest.VisibleContext,
	})
	if !engagement.ShouldLaunch {
		return launchRequest, memo, engagement.IgnoreReason
	}
	launchRequest.DecidedTurnFields = memo.decidedTurnFields()
	return launchRequest, memo, ""
}

func (agent *Agent) recordDecisionCalls(memo *messageDecision, taskRunID string) {
	if memo == nil || agent.taskRunStore == nil || strings.TrimSpace(taskRunID) == "" {
		return
	}
	for _, callRecord := range memo.callRecords {
		agent.taskRunStore.AppendLLMCall(taskRunID, callRecord)
	}
}
