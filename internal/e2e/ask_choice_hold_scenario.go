package e2e

import (
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func actionReplyExpectingAChoice(question string, choices string) string {
	return `{"action":"reply","expectsAnswer":true,"message":` + quote(question) + `,"choices":` + choices + `}`
}

func AskChoiceHoldAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "ask_choice_hold_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "ask_input"},
		Turns: []VirtualTurn{{
			Prompt:                 "회의실 잡아줘",
			RouterRequiredEvidence: []string{toolcontract.AskInputToolName},
			ActionResponses: []string{
				actionReplyExpectingAChoice("어느 회의실로 할까요?", `["회의실 A","회의실 B"]`),
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("ask_input"), BodyFragment: "회의실 A", Count: 1},
				{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: `"label":"회의실 B"`, Count: 1},
			},
			ExpectedEvents:         []string{agentcontract.TaskEventConfirmationRequested},
			ExpectedReplyFragments: []string{"어느 회의실로 할까요?", "회의실 B"},
			ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
		}, {
			Prompt:              "두 번째",
			ReplyTargetID:       "virtual-message-001",
			IsThread:            threadReply(),
			AnswersApprovalHold: true,
			RouterChoice:        "2",
			ActionResponses: []string{
				actionFinishMessage("회의실 B로 잡았습니다.", "obs-002"),
			},
			ExpectedEvents:         []string{agentcontract.TaskEventConfirmationReplyClassified, approvalrecord.TaskEventChoiceAnswered},
			ExpectedModelContexts:  []string{"The requester chose: 회의실 B"},
			ExpectedReplyFragments: []string{"회의실 B"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}
