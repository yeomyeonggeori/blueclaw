//go:build !nobundledharness

package e2e

import (
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func ScheduledRunAsksInDirectMessageScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return hostUpdateScenario("scheduled_run_asks_in_direct_message", artifactDirectoryPath, true, []VirtualTurn{{
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
		ActionResponses: []string{
			actionFinishMessage("업데이트를 시작했어요.", "obs-002"),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolRequestedEventName(virtualHostUpdateToolName), Count: 2},
			{Name: toolResultEventName(virtualHostUpdateToolName), BodyFragment: `"status":"started"`, Count: 1},
			{Name: agentcontract.TaskEventApprovalHoldSpent, BodyFragment: virtualHostLatestVersion, Count: 1},
			{Name: agentcontract.TaskEventApprovalHoldOpened, Count: 1},
		},
		ExpectedEvents:         []string{agentcontract.TaskEventConfirmationReplyClassified},
		ExpectedReplyFragments: []string{"업데이트"},
		ExpectedTaskStatus:     task.TaskStatusCompleted,
	}})
}
