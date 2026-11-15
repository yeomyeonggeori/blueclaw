//go:build !nobundledharness

package e2e

import (
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const virtualHandoffID = "virtual-handoff-1"

func BrowserHandoffScenario(artifactDirectoryPath string) VirtualSessionScenario {
	browserToolNames := []string{"browser_open", "browser_handoff"}
	return VirtualSessionScenario{
		Name:                  "browser_handoff",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          browserToolNames,
		RouterTaskShape:       agentcontract.TaskShapeResearchTask,
		CapabilityToolNames:   browserToolNames,
		InitialToolNames:      browserToolNames,
		Turns: []VirtualTurn{{
			Prompt:                 "https://portal.example.com 에서 지난달 청구서 받아줘",
			RouterRequiredEvidence: browserToolNames,
			ActionResponses: []string{
				actionCallTool("browser_open", `{"url":"https://portal.example.com/invoices"}`),
				actionCallTool("browser_handoff", `{"step":"sign in to the vendor portal"}`),
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: `"handoffID":"` + virtualHandoffID + `"`, Count: 1},
				{Name: agentcontract.TaskEventConfirmationRequested, BodyFragment: virtualHandoffID, Count: 0},
				{Name: agentcontract.TaskEventConfirmationRequested, BodyFragment: "sign in to the vendor portal", Count: 0},
				{Name: toolResultEventName("browser_handoff"), BodyFragment: `"ended"`, Count: 0},
			},
			ExpectedTaskStatus: task.TaskStatusWaitingApproval,
		}, {
			Prompt:         "다 했어",
			ReplyTargetID:  "virtual-message-001",
			IsThread:       threadReply(),
			RouterApproval: "approve",
			ActionResponses: []string{
				actionCallTool("browser_open", `{"url":"https://portal.example.com/invoices"}`),
				actionFinishMessage("로그인된 상태로 청구서 목록을 열었습니다.", "obs-003"),
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventApprovalHoldSpent, BodyFragment: `"handoff":"` + virtualHandoffID + `"`, Count: 1},
				{Name: toolResultEventName("browser_handoff"), BodyFragment: `\"ended\":\"done\"`, Count: 1},
			},
			ExpectedReplyFragments: []string{"청구서"},
		}},
	}
}

func virtualBrowserHandoffResponse(requestBody []byte) string {
	if !virtualInvokeContextOf(requestBody).carriesAnApproval() {
		message := "The requester has to do this step in the browser themselves. Nothing has been handed over yet: it is handed over once the hand-over is put to them."
		return virtualCapabilityJSON(map[string]any{
			"provider": "virtual", "selectedBackend": "device", "toolName": "browser_handoff",
			"outcome": "denied", "status": "denied", "isError": true,
			"content": message, "message": message, "errorCode": "approval_required", "failureStage": "authorization",
			"result": map[string]any{"errorCode": "approval_required", "failureStage": "authorization", "message": message, "approvalTarget": map[string]any{
				"inputField": "handoff",
				"id":         virtualHandoffID,
				"title":      "https://portal.example.com",
				"handoffID":  virtualHandoffID,
			}},
		})
	}
	return virtualCapabilitySuccess("browser_handoff", map[string]any{
		"ended":                "done",
		"origin":               "https://portal.example.com",
		"signInOrigin":         "https://portal.example.com",
		"signInCookiesChanged": true,
	})
}
