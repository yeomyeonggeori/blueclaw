package e2e

import (
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
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

func ScheduledRunAsksInDirectMessageScenario(artifactDirectoryPath string) VirtualSessionScenario {
	scenario := hostUpdateScenario("scheduled_run_asks_in_direct_message", artifactDirectoryPath, true, []VirtualTurn{{
		Prompt:           "지금 바로 업데이트해줘",
		RunsScheduledRun: true,
		ActionResponses: []string{
			actionCallTool(virtualHostUpdateToolName, `{"isRequestedNow":true}`),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolRequestedEventName(virtualHostUpdateToolName), Count: 1},
			{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: `"targetVersion":"` + virtualHostLatestVersion + `"`, Count: 1},
		},
		ExpectedEvents:        []string{agentcontract.TaskEventConfirmationRequested},
		ExpectedReplyTargetID: virtualDirectConversationID,
		ExpectedTaskStatus:    task.TaskStatusWaitingApproval,
	}, {
		Prompt:                 "1번, 지금 해",
		RepliesInDirectMessage: true,
		AnswersApprovalHold:    true,
		RouterChoice:           virtualHostNowChoiceKey,
		ExpectedResponse:       VirtualResponseBackgroundAction,
		ActionResponses: []string{
			actionFinishMessage("업데이트를 시작했어요.", observationOfTheCallInPlace),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolRequestedEventName(virtualHostUpdateToolName), Count: 1},
			{Name: toolResultEventName(virtualHostUpdateToolName), BodyFragment: `"status":"started"`, Count: 1},
			{Name: agentcontract.TaskEventApprovalHoldSpent, BodyFragment: virtualHostLatestVersion, Count: 1},
			{Name: agentcontract.TaskEventApprovalHoldOpened, Count: 1},
		},
		ExpectedEvents:     []string{agentcontract.TaskEventConfirmationReplyClassified},
		ExpectedTaskStatus: task.TaskStatusCompleted,
	}})
	scenario.AskInThread = true
	return scenario
}
