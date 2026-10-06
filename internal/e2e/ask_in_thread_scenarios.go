package e2e

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const (
	observationOfTheCarriedOutCall = "obs-002"
	observationOfTheCallInPlace    = "obs-001"
)

func AskedInThread(scenario VirtualSessionScenario) VirtualSessionScenario {
	scenario.AskInThread = true
	turns := make([]VirtualTurn, len(scenario.Turns))
	for index, virtualTurn := range scenario.Turns {
		turns[index] = virtualTurn
		if answersAnApproval(virtualTurn) {
			turns[index] = runningTheCallInPlace(virtualTurn)
		}
	}
	scenario.Turns = turns
	return scenario
}

func answersAnApproval(virtualTurn VirtualTurn) bool {
	return strings.TrimSpace(virtualTurn.RouterApproval) != "" || virtualTurn.AnswersApprovalHold
}

func runningTheCallInPlace(virtualTurn VirtualTurn) VirtualTurn {
	actionResponses := make([]string, len(virtualTurn.ActionResponses))
	for index, actionResponse := range virtualTurn.ActionResponses {
		actionResponses[index] = strings.ReplaceAll(actionResponse, observationOfTheCarriedOutCall, observationOfTheCallInPlace)
	}
	virtualTurn.ActionResponses = actionResponses
	virtualTurn.ExpectedEventCounts = askedOnceEventCounts(virtualTurn.ExpectedEventCounts)
	return virtualTurn
}

func askedOnceEventCounts(expectedEventCounts []VirtualEventCount) []VirtualEventCount {
	adjusted := make([]VirtualEventCount, len(expectedEventCounts))
	for index, expectedEventCount := range expectedEventCounts {
		adjusted[index] = expectedEventCount
		if isToolRequestedEvent(expectedEventCount.Name) && expectedEventCount.Count == 2 {
			adjusted[index].Count = 1
		}
	}
	return adjusted
}

func isToolRequestedEvent(eventName string) bool {
	return strings.HasPrefix(eventName, agentcontract.ToolTaskEventPrefix) && strings.HasSuffix(eventName, agentcontract.ToolTaskEventRequestedSuffix)
}
