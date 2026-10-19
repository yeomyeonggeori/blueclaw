//go:build !nobundledharness

package e2e

import (
	"encoding/json"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const virtualCRMImportFilePath = "shared/inbox/clients.csv"

const virtualCRMImportFileContent = "회사명,주소\n샘플상사,서울\n예시물산,부산\n"

var virtualCRMImportInputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["organization","contact"]},"attachments":{"type":"array","items":{"type":"string"}},"columns":{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string"},"address":{"type":"string"}}},"onDuplicate":{"type":"string","enum":["skip","update"]}},"required":["kind"]}`)

var virtualCRMImportInputIntentSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["organization","contact"]},"attachments":{"type":"array","items":{"type":"string"}},"columns":{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string"},"address":{"type":"string"}}},"onDuplicate":{"type":"string","enum":["skip","update"]}}}`)

func virtualCRMImportDescriptor() agentruntime.CapabilityToolDescriptor {
	return agentruntime.CapabilityToolDescriptor{
		Name:              virtualCRMImportToolName,
		RequiresApproval:  true,
		ModelVisibility:   toolcontract.ToolVisibilityModel,
		InputSchema:       virtualCRMImportInputSchema,
		InputIntentSchema: virtualCRMImportInputIntentSchema,
	}
}

func virtualCRMImportScenario(name string, artifactDirectoryPath string, prompt string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                      name,
		ArtifactDirectoryPath:     artifactDirectoryPath,
		RouterRequiredEvidence:    []string{virtualCRMImportToolName},
		AllowedTools:              append(agentruntime.KernelToolNames(), virtualCRMImportToolName),
		CapabilityToolNames:       []string{virtualCRMImportToolName},
		InitialToolNames:          []string{virtualCRMImportToolName},
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{virtualCRMImportDescriptor()},
		InitialWorkspaceFiles:     map[string]string{virtualCRMImportFilePath: virtualCRMImportFileContent},
		ScriptedExecutionPlan: &agentcontract.ExecutionPlan{
			OriginalInstruction:     prompt,
			Summary:                 "첨부한 CSV의 관계처를 CRM으로 가져온다",
			MissingInformation:      []string{},
			ContinuationInstruction: "첨부한 CSV의 관계처를 CRM으로 가져온다",
		},
	}
}

func CRMImportFromAttachmentAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	prompt := "방금 보낸 clients.csv로 관계처 가져와줘"
	scenario := virtualCRMImportScenario("crm_import_from_attachment_acceptance", artifactDirectoryPath, prompt)
	importCall := `{"kind":"organization","attachments":["/workspace/` + virtualCRMImportFilePath + `"],"columns":{"name":"회사명","address":"주소"}}`
	scenario.Turns = []VirtualTurn{{
		Prompt:          prompt,
		ActionResponses: []string{actionCallTool(virtualCRMImportToolName, importCall)},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolRequestedEventName(virtualCRMImportToolName), BodyFragment: `"columns":{"name":"회사명"`, Count: 1},
			{Name: toolRequestedEventName(virtualCRMImportToolName), BodyFragment: "샘플상사", Count: 0},
			{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: virtualCRMImportToolName, Count: 1},
			{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: "샘플상사", Count: 0},
		},
		ExpectedEvents:         []string{agentcontract.TaskEventConfirmationRequested},
		ExpectedReplyFragments: []string{"clients.csv"},
		ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
	}, {
		Prompt:         "응 가져와",
		ReplyTargetID:  "virtual-message-001",
		IsThread:       threadReply(),
		RouterApproval: "approve",
		ActionResponses: []string{
			actionFinishMessage("관계처 2곳을 가져왔습니다.", "obs-002"),
		},
		ExpectedToolCalls: []string{virtualCRMImportToolName},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolResultEventName(virtualCRMImportToolName), BodyFragment: `"created":2`, Count: 1},
			{Name: agentcontract.TaskEventApprovalHoldSpent, BodyFragment: virtualCRMImportToolName, Count: 1},
		},
		ExpectedReplyFragments: []string{"2곳", "가져왔습니다"},
	}}
	return scenario
}

func CRMImportFromUnreadableFileScenario(artifactDirectoryPath string) VirtualSessionScenario {
	prompt := "missing.csv로 관계처 가져와줘"
	scenario := virtualCRMImportScenario("crm_import_from_unreadable_file", artifactDirectoryPath, prompt)
	scenario.Turns = []VirtualTurn{{
		Prompt: prompt,
		ActionResponses: []string{
			actionCallTool(virtualCRMImportToolName, `{"kind":"organization","attachments":["/workspace/shared/inbox/missing.csv"],"columns":{"name":"회사명"}}`),
			actionFinishMessage("missing.csv 파일을 읽을 수 없어 가져오지 못했습니다."),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolResultEventName(virtualCRMImportToolName), BodyFragment: "missing.csv", Count: 1},
			{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: "", Count: 0},
		},
		ForbiddenEvents:        []string{agentcontract.TaskEventConfirmationRequested},
		ExpectedReplyFragments: []string{"missing.csv", "읽을 수 없어"},
		ExpectedTaskStatus:     task.TaskStatusCompleted,
	}}
	return scenario
}
