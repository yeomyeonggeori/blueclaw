package e2e

import (
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const steerInstruction = "제목 앞에 '긴급'을 붙여줘"

func SteerWhileRunningAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "steer_while_running_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"write"},
		InitialToolNames:      []string{"write"},
		Turns: []VirtualTurn{{
			Prompt:                 "고객지원 FAQ 개편 메모를 파일로 만들어줘.",
			RouterRequiredEvidence: []string{"write"},
			SteerAfterFirstAction:  steerInstruction,
			ActionResponses: []string{
				actionCallTool("write", `{"path":"work/customer-support/faq-memo.txt","content":"FAQ 개편\n"}`),
				actionFinishMessage("제목에 긴급을 붙여 메모를 저장했습니다.", "obs-001"),
			},
			ExpectedToolCalls: []string{"write"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventTaskSteerRequested, BodyFragment: "긴급", Count: 1},
			},
			ExpectedModelContexts:  []string{steerInstruction},
			ExpectedReplyFragments: []string{"긴급"},
		}},
	}
}

func (harness *VirtualSessionHarness) steerAfterFirstAction(instruction string) func(actionsServed int) {
	if instruction == "" {
		return nil
	}
	return func(actionsServed int) {
		if actionsServed != 1 {
			return
		}
		for _, taskRun := range harness.taskRunService.ListTaskRunByPersonID("person-1") {
			harness.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventTaskSteerRequested, agentruntime.MarshalBody(map[string]string{
				"messageID":   "virtual-steer-001",
				"instruction": instruction,
				"reason":      "the person corrected the request while it ran",
			}))
		}
	}
}
