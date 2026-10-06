package connectors

import (
	"context"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

type scriptedGatewayDecider struct {
	addressing          agentcontract.AddressingDecision
	busyRoute           agentcontract.BusyRoute
	relatesToActiveTask bool
	errorValue          error

	mutex     sync.Mutex
	callCount int
	requests  []inboundengagement.Facts
}

func (decider *scriptedGatewayDecider) Decide(_ context.Context, facts inboundengagement.Facts, _ agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	decider.mutex.Lock()
	defer decider.mutex.Unlock()
	decider.callCount++
	decider.requests = append(decider.requests, facts)
	if decider.errorValue != nil {
		return nil, decider.errorValue
	}
	return judgmentsFor(facts, decider.addressing, decider.busyRoute, decider.relatesToActiveTask), nil
}

func (decider *scriptedGatewayDecider) FitsBurstBudget(inboundengagement.Facts) bool {
	return true
}

func (decider *scriptedGatewayDecider) calls() int {
	decider.mutex.Lock()
	defer decider.mutex.Unlock()
	return decider.callCount
}

func (decider *scriptedGatewayDecider) lastFacts() inboundengagement.Facts {
	decider.mutex.Lock()
	defer decider.mutex.Unlock()
	return decider.requests[len(decider.requests)-1]
}

func (decider *scriptedGatewayDecider) decidedPrompt(prompt string) (inboundengagement.Facts, bool) {
	decider.mutex.Lock()
	defer decider.mutex.Unlock()
	for _, facts := range decider.requests {
		for _, message := range facts.Messages {
			if message.Prompt == prompt {
				return facts, true
			}
		}
	}
	return inboundengagement.Facts{}, false
}

func judgmentsFor(facts inboundengagement.Facts, addressing agentcontract.AddressingDecision, busyRoute agentcontract.BusyRoute, relatesToActiveTask bool) []inboundengagement.Judgment {
	judgments := []inboundengagement.Judgment{}
	for _, message := range facts.Messages {
		judgment := inboundengagement.Judgment{MessageID: message.MessageID, Addressing: addressing}
		if facts.OpenTask != nil {
			judgment.BusyRoute = busyRoute
		}
		if facts.OpenTask != nil || facts.FinishedTask != nil {
			judgment.HasRelatesToActiveTask = true
			judgment.RelatesToActiveTask = relatesToActiveTask
		}
		judgments = append(judgments, judgment)
	}
	return judgments
}

type harnessGatewayDecider struct {
	harness *harnesstest.Harness
}

func harnessGateway(harness *harnesstest.Harness) harnessGatewayDecider {
	return harnessGatewayDecider{harness: harness}
}

func (decider harnessGatewayDecider) Decide(_ context.Context, facts inboundengagement.Facts, _ agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	return judgmentsFor(facts, decider.harness.AddressingDecision, decider.harness.TurnDecision.BusyRoute, decider.harness.IsActiveTaskFollowUp), nil
}

func (decider harnessGatewayDecider) FitsBurstBudget(inboundengagement.Facts) bool {
	return true
}
