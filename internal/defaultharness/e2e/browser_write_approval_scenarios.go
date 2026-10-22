//go:build !nobundledharness

package e2e

import (
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const virtualWithheldRequestID = "virtual-withheld-1"

const virtualWithheldRequestPreview = "POST https://bank.example/transfer\nto: 박예시\namount: 1000"

func BrowserWriteApprovalScenario(artifactDirectoryPath string) VirtualSessionScenario {
	browserToolNames := []string{"browser_open", "browser_click"}
	return VirtualSessionScenario{
		Name:                  "browser_write_approval",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          browserToolNames,
		RouterTaskShape:       agentcontract.TaskShapeResearchTask,
		CapabilityToolNames:   browserToolNames,
		InitialToolNames:      browserToolNames,
		Turns: []VirtualTurn{{
			Prompt:                 "https://bank.example/account 열어서 확인 버튼 눌러줘",
			RouterRequiredEvidence: browserToolNames,
			ActionResponses: []string{
				actionCallTool("browser_open", `{"url":"https://bank.example/account"}`),
				actionCallTool("browser_click", `{"target":"@e1"}`),
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: `"withheldRequest":"` + virtualWithheldRequestID + `"`, Count: 1},
				{Name: agentcontract.TaskEventConfirmationRequested, BodyFragment: "to: 박예시", Count: 1},
				{Name: agentcontract.TaskEventConfirmationRequested, BodyFragment: "POST https://bank.example/transfer", Count: 1},
				{Name: toolResultEventName("browser_click"), BodyFragment: `"action":"click"`, Count: 0},
			},
			ExpectedTaskStatus: task.TaskStatusWaitingApproval,
		}, {
			Prompt:         "응 보내",
			ReplyTargetID:  "virtual-message-001",
			IsThread:       threadReply(),
			RouterApproval: "approve",
			ActionResponses: []string{
				actionFinishMessage("승인하신 요청을 보냈습니다.", "obs-002"),
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventApprovalHoldSpent, BodyFragment: `"withheldRequest":"` + virtualWithheldRequestID + `"`, Count: 1},
				{Name: toolResultEventName("browser_click"), BodyFragment: `"action":"click"`, Count: 1},
			},
			ExpectedReplyFragments: []string{"보냈습니다"},
		}},
	}
}

func virtualBrowserClickResponse(requestBody []byte) string {
	if !virtualInvokeContextOf(requestBody).carriesAnApproval() {
		message := "This action made the page send POST https://bank.example, which changes data. Nothing was sent: it goes out only if the requester approves exactly this."
		return virtualCapabilityJSON(map[string]any{
			"provider": "virtual", "selectedBackend": "device", "toolName": "browser_click",
			"outcome": "denied", "status": "denied", "isError": true,
			"content": message, "message": message, "errorCode": "approval_required", "failureStage": "authorization",
			"result": map[string]any{"errorCode": "approval_required", "failureStage": "authorization", "message": message, "withheldRequestID": virtualWithheldRequestID},
		})
	}
	return virtualCapabilitySuccess("browser_click", virtualBrowserResult("browser_click", virtualCapabilityInput(requestBody)))
}

func virtualBrowserClickTargetResponse() string {
	return virtualCapabilityTargetResolved("browser_click", map[string]any{
		"inputField": "withheldRequest",
		"id":         virtualWithheldRequestID,
		"title":      "POST https://bank.example",
		"preview":    virtualWithheldRequestPreview,
	})
}
