//go:build !nobundledharness

package e2e

import (
	"slices"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const (
	virtualTaskWriteForbiddenError = "task_write_forbidden"
	virtualWorkForOthersRefusal    = "work given only to other people is added as a request; recording it in another state is an administrator's"
)

var virtualStatusesAMemberGivesOthers = []string{"", "planned", "requested"}

// refusedWorkForOthers answers the way internkim's task_save does
// (supabase/migrations/20261020000102_a_task_says_who_may_change_it.sql).
func (service *virtualCapabilityService) refusedWorkForOthers(requestBody []byte, input map[string]any) (string, bool) {
	requester := virtualCapabilityRequesterFromRequest(requestBody)
	participantHints := stringSliceValue(input["participantPersonHints"])
	if len(participantHints) == 0 || slices.ContainsFunc(participantHints, requester.isNamedBy) {
		return "", false
	}
	if service.isAdmin(requester.PersonID) {
		return "", false
	}
	if !slices.Contains(virtualStatusesAMemberGivesOthers, strings.TrimSpace(stringValue(input["status"]))) {
		return virtualWorkForOthersRefusalResponse(), true
	}
	input["status"] = "requested"
	return "", false
}

func (requester virtualCapabilityRequester) isNamedBy(hint string) bool {
	named := strings.TrimPrefix(strings.TrimSpace(hint), "@")
	localPart, _, _ := strings.Cut(requester.Email, "@")
	return named != "" && (named == requester.Name || strings.EqualFold(named, requester.Email) || strings.EqualFold(named, localPart))
}

func virtualWorkForOthersRefusalResponse() string {
	return virtualCapabilityJSON(map[string]any{
		"provider": "virtual", "selectedBackend": "device", "toolName": "task_add",
		"outcome": "failed", "status": "error", "isError": true,
		"content": virtualWorkForOthersRefusal, "message": virtualWorkForOthersRefusal,
		"errorCode": virtualTaskWriteForbiddenError, "failureStage": "execution",
		"result": map[string]any{"error": virtualWorkForOthersRefusal, "errorCode": virtualTaskWriteForbiddenError, "retryable": false, "safeRetry": true},
	})
}

func virtualMeetingAnnouncement(meetingTitle string) connectors.VisibleContextMessage {
	return connectors.VisibleContextMessage{
		Speaker:            "샘플",
		SpeakerCallingName: "샘플 님",
		SpeakerHandle:      "sample",
		Text:               "https://meet.example.com/abc-defg-hij @박예시 @최견본 " + meetingTitle + "입니다.",
	}
}

var virtualAgentIdentity = agentcontract.AgentIdentity{Name: "김인턴", Handle: "김인턴"}

var virtualCompletedWorkPrompt = "@" + virtualAgentIdentity.Handle + " 완료 업무로 추가해줘"

var virtualRegisteredTaskTypes = []string{"회의", "문서"}

var virtualMeetingTitle = virtualUnregisteredBusiness + " 소회의"

func completedMeetingScenario(name string, artifactDirectoryPath string, turn VirtualTurn) VirtualSessionScenario {
	toolNames := []string{"task_add", "task_list", "task_update"}
	turn.ContextMessages = []connectors.VisibleContextMessage{virtualMeetingAnnouncement(virtualMeetingTitle)}
	turn.Prompt = virtualCompletedWorkPrompt
	turn.IsThread = threadReply()
	turn.RouterRequiredEvidence = []string{"task_add"}
	turn.ExpectedModelContexts = []string{virtualMeetingTitle}
	return VirtualSessionScenario{
		Name:                  name,
		ArtifactDirectoryPath: artifactDirectoryPath,
		InitialTaskBusinesses: virtualRegisteredBusinesses,
		InitialTaskTypes:      virtualRegisteredTaskTypes,
		Skills:                []agentcontract.SkillInstruction{flowTaskSkill()},
		AllowedTools:          append([]string{"conversation_history", "memory_search"}, toolNames...),
		CapabilityToolNames:   toolNames,
		InitialToolNames:      []string{"task_add"},
		AgentIdentity:         virtualAgentIdentity,
		Turns:                 []VirtualTurn{turn},
	}
}

func CompletedMeetingFromEarlierMessageScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return completedMeetingScenario("completed_meeting_from_earlier_message", artifactDirectoryPath, VirtualTurn{
		ActionResponses: []string{
			actionInvokeCapabilityTool("task_add", `{"title":"`+virtualMeetingTitle+`","business":"`+virtualUnregisteredBusiness+`","status":"completed","participantPersonHints":["박예시","최견본"]}`),
			actionInvokeCapabilityTool("task_add", `{"title":"`+virtualMeetingTitle+`","status":"completed","participantPersonHints":["박예시","최견본"]}`),
			actionInvokeCapabilityTool("task_add", `{"title":"`+virtualMeetingTitle+`","status":"completed","participantPersonHints":["샘플","박예시","최견본"]}`),
			actionFinishMessage(virtualMeetingTitle+"을 완료 업무로 추가했어요.", "obs-005"),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolResultEventName("task_add"), BodyFragment: virtualUnregisteredBusiness + " is not one of", Count: 1},
			{Name: toolResultEventName("task_add"), BodyFragment: virtualTaskWriteForbiddenError, Count: 1},
			{Name: toolResultEventName("task_add"), BodyFragment: `"effect":"created"`, Count: 1},
		},
		ExpectedReplyFragments: []string{virtualMeetingTitle},
		ExpectedTaskStatus:     task.TaskStatusCompleted,
	})
}

func CompletedMeetingRecordUnreachableScenario(artifactDirectoryPath string) VirtualSessionScenario {
	failure := "업무 기록에 연결되지 않아 " + virtualMeetingTitle + "을 추가하지 못했어요."
	scenario := completedMeetingScenario("completed_meeting_record_unreachable", artifactDirectoryPath, VirtualTurn{
		ActionResponses: []string{
			actionInvokeCapabilityTool("task_add", `{"title":"`+virtualMeetingTitle+`","status":"completed"}`),
			actionFailMessage(failure),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolResultEventName("task_add"), BodyFragment: "the record could not be reached", MinCount: 1},
			{Name: toolResultEventName("task_add"), BodyFragment: `"effect":"created"`, Count: 0},
		},
		ExpectedReplyFragments: []string{failure},
		ExpectedTaskStatus:     task.TaskStatusFailed,
	})
	scenario.UnreachableToolNames = []string{"task_add"}
	return scenario
}

func CompletedMeetingForAnUnregisteredBusinessScenario(artifactDirectoryPath string) VirtualSessionScenario {
	scenario := completedMeetingScenario("completed_meeting_for_an_unregistered_business", artifactDirectoryPath, VirtualTurn{
		ActionResponses: []string{
			actionInvokeCapabilityTool("task_add", `{"title":"`+virtualMeetingTitle+`","business":"`+virtualUnregisteredBusiness+`","status":"completed"}`),
			actionInvokeCapabilityTool("task_add", `{"title":"`+virtualMeetingTitle+`","status":"completed"}`),
			actionFinishMessage(virtualMeetingTitle+"을 완료 업무로 추가했어요. "+virtualUnregisteredBusiness+" 사업은 관리자가 등록하면 붙일 수 있어요.", "obs-003"),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolResultEventName("task_add"), BodyFragment: virtualUnregisteredBusiness + " is not one of", Count: 1},
			{Name: toolResultEventName("task_add"), BodyFragment: `"effect":"created"`, Count: 1},
		},
		ExpectedReplyFragments: []string{virtualMeetingTitle},
		ExpectedTaskStatus:     task.TaskStatusCompleted,
	})
	scenario.Turns[0].ContextMessages = append([]connectors.VisibleContextMessage{{
		Speaker:            "샘플",
		SpeakerCallingName: "샘플 님",
		SpeakerHandle:      "sample",
		Text:               virtualUnregisteredBusiness + " 사업은 제가 맡고 있어요.",
	}}, scenario.Turns[0].ContextMessages...)
	return scenario
}
