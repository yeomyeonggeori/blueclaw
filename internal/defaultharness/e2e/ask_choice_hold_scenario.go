//go:build !nobundledharness

package e2e

import (
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
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
			ExpectedEvents:          []string{agentcontract.TaskEventConfirmationRequested},
			ExpectedReplyFragments:  []string{"어느 회의실로 할까요?"},
			ExpectedChoiceLabels:    []string{"회의실 A", "회의실 B"},
			ForbiddenReplyFragments: []string{"1. 회의실 A"},
			ExpectedTaskStatus:      task.TaskStatusWaitingApproval,
		}, {
			Prompt:              "두 번째",
			ReplyTargetID:       "virtual-message-001",
			IsThread:            threadReply(),
			AnswersApprovalHold: true,
			RouterChoice:        "2",
			ActionResponses: []string{
				actionFinishMessage("회의실 B로 잡았습니다.", "obs-002"),
			},
			ExpectedEvents:         []string{agentcontract.TaskEventConfirmationReplyClassified, approvalgate.TaskEventChoiceAnswered},
			ExpectedModelContexts:  []string{"The requester chose: 회의실 B"},
			ExpectedReplyFragments: []string{"회의실 B"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func AskChoiceOwnWordsAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	scenario := AskChoiceHoldAcceptanceScenario(artifactDirectoryPath)
	scenario.Name = "ask_choice_own_words_acceptance"
	scenario.Turns[1] = VirtualTurn{
		Prompt:              "둘 다 말고 회의실 C로 잡아줘, 열 명이야",
		ReplyTargetID:       "virtual-message-001",
		IsThread:            threadReply(),
		AnswersApprovalHold: true,
		RouterChoice:        approvalreply.OwnWordsOptionID,
		ActionResponses: []string{
			actionFinishMessage("회의실 C로 잡았습니다.", "obs-002"),
		},
		ExpectedEvents:         []string{approvalgate.TaskEventChoiceAnswered},
		ForbiddenEvents:        []string{agentcontract.TaskEventTaskFailed},
		ExpectedModelContexts:  []string{"The requester answered in their own words: 둘 다 말고 회의실 C로 잡아줘, 열 명이야"},
		ExpectedReplyFragments: []string{"회의실 C"},
		ExpectedTaskStatus:     task.TaskStatusCompleted,
	}
	return scenario
}
