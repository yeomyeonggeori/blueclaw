package e2e

import (
	"encoding/json"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

var virtualHostUpdateInputSchema = json.RawMessage(`{"type":"object","properties":{"targetVersion":{"type":"string"},"startsAt":{"type":"string"},"isRequestedNow":{"type":"boolean"}},"additionalProperties":false}`)

var virtualHostUpdateResultSchema = json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"},"fromVersion":{"type":"string"},"toVersion":{"type":"string"},"isScheduled":{"type":"boolean"}},"additionalProperties":false}`)

func hostUpdateScenario(name string, artifactDirectoryPath string, isRequesterAdmin bool, turns []VirtualTurn) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   name,
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RequesterIsAdmin:       isRequesterAdmin,
		RouterRequiredEvidence: []string{virtualHostUpdateToolName},
		AllowedTools:           append(agentruntime.KernelToolNames(), virtualHostUpdateToolName),
		InitialToolNames:       []string{virtualHostUpdateToolName},
		CapabilityToolNames:    []string{virtualHostUpdateToolName},
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{{
			Name:              virtualHostUpdateToolName,
			RequiresApproval:  true,
			InputSchema:       virtualHostUpdateInputSchema,
			InputIntentSchema: virtualHostUpdateInputSchema,
			ResultContract:    &agentruntime.CapabilityToolResultContract{Schema: virtualHostUpdateResultSchema},
		}},
		Turns: turns,
	}
}

func HostUpdateNowAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return hostUpdateScenario("host_update_now_acceptance", artifactDirectoryPath, true, []VirtualTurn{{
		Prompt: "지금 바로 업데이트해줘",
		ActionResponses: []string{
			actionCallTool(virtualHostUpdateToolName, `{"isRequestedNow":true}`),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolRequestedEventName(virtualHostUpdateToolName), Count: 1},
			{Name: agentcontract.TaskEventApprovalPendingCall, BodyFragment: `"targetVersion":"` + virtualHostLatestVersion + `"`, Count: 1},
			{Name: approvalgate.TaskEventApprovalChoicesOffered, BodyFragment: `"choices":[{"key":"now"},{"key":"offHours","startsAt":"` + virtualHostOffHoursStartsAt + `"}]`, Count: 1},
		},
		ExpectedEvents:     []string{agentcontract.TaskEventConfirmationRequested},
		ExpectedTaskStatus: task.TaskStatusWaitingApproval,
	}, {
		Prompt:       "1번, 지금 해",
		RouterChoice: virtualHostNowChoiceKey,
		ActionResponses: []string{
			actionFinishMessage("업데이트를 시작했어요.", "obs-002"),
		},
		ExpectedToolCalls: []string{virtualHostUpdateToolName},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolResultEventName(virtualHostUpdateToolName), BodyFragment: `"status":"started"`, Count: 1},
			{Name: agentcontract.TaskEventApprovalExecuted, BodyFragment: virtualHostLatestVersion, Count: 1},
			{Name: approvalgate.TaskEventApprovalDeferred, Count: 0},
		},
		ExpectedEvents:         []string{agentcontract.TaskEventConfirmationReplyClassified},
		ExpectedReplyFragments: []string{"업데이트"},
	}})
}

func HostUpdateOffHoursAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return hostUpdateScenario("host_update_off_hours_acceptance", artifactDirectoryPath, true, []VirtualTurn{{
		Prompt: "업데이트해줘",
		ActionResponses: []string{
			actionCallTool(virtualHostUpdateToolName, `{}`),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: approvalgate.TaskEventApprovalChoicesOffered, BodyFragment: `"choices":[{"key":"offHours","startsAt":"` + virtualHostOffHoursStartsAt + `"},{"key":"now"}]`, Count: 1},
		},
		ExpectedEvents:     []string{agentcontract.TaskEventConfirmationRequested},
		ExpectedTaskStatus: task.TaskStatusWaitingApproval,
	}, {
		Prompt:       "새벽에 해줘",
		RouterChoice: virtualHostOffHoursChoiceKey,
		ActionResponses: []string{
			actionFinishMessage("새벽 3시에 업데이트하도록 잡아 두었어요.", "obs-002"),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: approvalgate.TaskEventApprovalDeferred, BodyFragment: `"startsAt":"` + virtualHostOffHoursStartsAt + `"`, Count: 1},
			{Name: toolResultEventName(virtualHostUpdateToolName), BodyFragment: `"status":"started"`, Count: 0},
			{Name: agentcontract.TaskEventApprovalExecuted, Count: 0},
		},
		ExpectedModelContexts:  []string{"has not run yet"},
		ExpectedReplyFragments: []string{"새벽 3시"},
	}, {
		FiresDueApprovedSchedules: true,
		ActionResponses: []string{
			actionFinishMessage("예약한 업데이트를 시작했어요.", "obs-001"),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: agentruntime.TaskEventScheduledApprovedCallCarriedOut, BodyFragment: `"targetVersion":"` + virtualHostLatestVersion + `"`, Count: 1},
			{Name: toolResultEventName(virtualHostUpdateToolName), BodyFragment: `"isScheduled":true`, Count: 1},
			{Name: agentcontract.TaskEventApprovalPendingCall, Count: 0},
			{Name: agentcontract.TaskEventConfirmationRequested, Count: 0},
		},
		ExpectedReplyFragments: []string{"업데이트"},
	}})
}

func HostUpdateMemberRefusedScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return hostUpdateScenario("host_update_member_refused", artifactDirectoryPath, false, []VirtualTurn{{
		Prompt: "업데이트해줘",
		ActionResponses: []string{
			actionCallTool(virtualHostUpdateToolName, `{}`),
			actionRefusedMessage("호스트 업데이트는 관리자만 할 수 있어요."),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolResultEventName(virtualHostUpdateToolName), BodyFragment: "administrator", Count: 1},
			{Name: toolResultEventName(virtualHostUpdateToolName), BodyFragment: `"status":"started"`, Count: 0},
			{Name: agentcontract.TaskEventApprovalPendingCall, Count: 0},
			{Name: agentcontract.TaskEventConfirmationRequested, Count: 0},
		},
		ExpectedReplyFragments: []string{"관리자"},
		ExpectedTaskStatus:     task.TaskStatusFailed,
	}})
}

func actionRefusedMessage(reply string) string {
	return `{"action":"fail","message":` + quote(reply) + `,"reason":` + quote(reply) + `,"goalStatus":"blocked","goalSatisfied":false,"remainingWork":"Only an administrator can update the host.","failureResolution":"failure_report","usedFailureFacts":{"attempts":[{"toolName":"host_update","inputSummary":"{}","errorCode":"access_denied","failureStage":"authorization","message":"only an administrator of this company can update its host"}],"budgetState":"no_tool_fallback_available"},"executionStateUpdate":{}}`
}
