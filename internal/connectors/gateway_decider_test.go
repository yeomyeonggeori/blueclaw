package connectors

import (
	"context"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
)

type scriptedGatewayDecider struct {
	addressing          inboundengagement.AddressingDecision
	busyRoute           inboundengagement.BusyRoute
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

func judgmentsFor(facts inboundengagement.Facts, addressing inboundengagement.AddressingDecision, busyRoute inboundengagement.BusyRoute, relatesToActiveTask bool) []inboundengagement.Judgment {
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

type scriptedHarness struct {
	*harnesstest.Harness
	AddressingDecision   inboundengagement.AddressingDecision
	BusyRoute            inboundengagement.BusyRoute
	IsActiveTaskFollowUp bool
}

type harnessGatewayDecider struct {
	harness agentcontract.Harness
}

func harnessGateway(harness agentcontract.Harness) harnessGatewayDecider {
	return harnessGatewayDecider{harness: harness}
}

func (decider harnessGatewayDecider) Decide(_ context.Context, facts inboundengagement.Facts, _ agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	scripted, isScripted := decider.harness.(*scriptedHarness)
	if !isScripted {
		return judgmentsFor(facts, addressedToBot(), "", false), nil
	}
	return judgmentsFor(facts, scriptedAddressingOrAddressedToBot(scripted.AddressingDecision), scripted.BusyRoute, scripted.IsActiveTaskFollowUp), nil
}

func scriptedAddressingOrAddressedToBot(addressing inboundengagement.AddressingDecision) inboundengagement.AddressingDecision {
	if addressing.Target == "" {
		return addressedToBot()
	}
	return addressing
}

func (decider harnessGatewayDecider) FitsBurstBudget(inboundengagement.Facts) bool {
	return true
}
