//go:build !nobundledharness

package e2e

import (
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const (
	virtualTaskVocabularyToolName       = "task_vocabulary_set"
	virtualTaskLabelUnregisteredError   = "task_label_unregistered"
	virtualTaskVocabularyNotAdminError  = "record_refused"
	virtualTaskVocabularyNotAdminReason = "only an active company admin can save task definitions"
	virtualUnregisteredBusiness         = "블루세이지"
)

var virtualRegisteredBusinesses = []string{"노스필드", "하버라인"}

type virtualTaskVocabulary struct {
	businesses []map[string]any
	types      []map[string]any
}

func newVirtualTaskVocabulary(businessNames []string, typeNames []string) virtualTaskVocabulary {
	return virtualTaskVocabulary{businesses: namedLabels(businessNames), types: namedLabels(typeNames)}
}

func namedLabels(names []string) []map[string]any {
	labels := make([]map[string]any, 0, len(names))
	for _, name := range names {
		labels = append(labels, map[string]any{"name": name})
	}
	return labels
}

func (vocabulary virtualTaskVocabulary) registeredLabels() map[string]any {
	labels := virtualTaskRegisteredLabels()
	labels["businesses"] = nonNilLabels(vocabulary.businesses)
	labels["types"] = nonNilLabels(vocabulary.types)
	return labels
}

func nonNilLabels(labels []map[string]any) []map[string]any {
	if labels == nil {
		return []map[string]any{}
	}
	return labels
}

func labelNames(labels []map[string]any) []string {
	names := make([]string, 0, len(labels))
	for _, label := range labels {
		names = append(names, stringValue(label["name"]))
	}
	return names
}

// resolveVirtualTaskLabel answers the way internkim's record labelOf does
// (web/src/lib/server/public-api/record/labels.ts): an empty registry takes any
// label, otherwise the label must name exactly one registered entry.
func resolveVirtualTaskLabel(registered []map[string]any, asked string) (string, bool) {
	written := strings.TrimSpace(asked)
	names := labelNames(registered)
	if written == "" || len(names) == 0 {
		return written, true
	}
	contained := []string{}
	for _, name := range names {
		if strings.EqualFold(name, written) {
			return name, true
		}
		if strings.Contains(strings.ToLower(name), strings.ToLower(written)) {
			contained = append(contained, name)
		}
	}
	if len(contained) == 1 {
		return contained[0], true
	}
	return "", false
}

func (service *virtualCapabilityService) refusedTaskLabel(toolName string, input map[string]any) (string, bool) {
	for fieldName, registered := range map[string][]map[string]any{"business": service.taskVocabulary.businesses, "type": service.taskVocabulary.types} {
		asked, isAsked := input[fieldName]
		if !isAsked {
			continue
		}
		label, isResolved := resolveVirtualTaskLabel(registered, stringValue(asked))
		if !isResolved {
			return virtualTaskLabelRefusal(toolName, stringValue(asked), labelNames(registered)), true
		}
		input[fieldName] = label
	}
	return "", false
}

func virtualTaskLabelRefusal(toolName string, asked string, registered []string) string {
	message := asked + " is not one of " + strings.Join(registered, ", ")
	return virtualCapabilityJSON(map[string]any{
		"provider": "virtual", "selectedBackend": "device", "toolName": toolName,
		"outcome": "failed", "status": "error", "isError": true,
		"content": message, "message": message, "errorCode": virtualTaskLabelUnregisteredError, "failureStage": "input_validation",
		"result": map[string]any{"message": message},
	})
}

func (service *virtualCapabilityService) taskVocabularyResponse(requestBody []byte) string {
	if !service.isAdmin(virtualInvokeContextOf(requestBody).RequesterPersonID) {
		return virtualTaskVocabularyRefusal()
	}
	input := virtualCapabilityInput(requestBody)
	service.taskVocabulary = virtualTaskVocabulary{
		businesses: virtualWrittenLabels(input["businesses"]),
		types:      virtualWrittenLabels(input["types"]),
	}
	return virtualCapabilityJSON(map[string]any{
		"provider": "virtual", "selectedBackend": "device", "toolName": virtualTaskVocabularyToolName,
		"outcome": "succeeded", "status": "ok",
		"result":  service.taskVocabulary.registeredLabels(),
		"effects": []map[string]any{{"objectType": "task_vocabulary", "effect": "updated"}},
	})
}

func virtualWrittenLabels(value any) []map[string]any {
	items, _ := value.([]any)
	labels := []map[string]any{}
	for _, item := range items {
		label, isLabel := item.(map[string]any)
		if isLabel && strings.TrimSpace(stringValue(label["name"])) != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func virtualTaskVocabularyRefusal() string {
	return virtualCapabilityJSON(map[string]any{
		"provider": "virtual", "selectedBackend": "device", "toolName": virtualTaskVocabularyToolName,
		"outcome": "denied", "status": "denied", "isError": true,
		"content": virtualTaskVocabularyNotAdminReason, "message": virtualTaskVocabularyNotAdminReason,
		"errorCode": virtualTaskVocabularyNotAdminError, "failureStage": "authorization",
		"result": map[string]any{"message": virtualTaskVocabularyNotAdminReason},
	})
}

func taskBusinessRegistrationScenario(name string, artifactDirectoryPath string, isRequesterAdmin bool, registrationTurn VirtualTurn) VirtualSessionScenario {
	toolNames := []string{"task_add", "task_list", "task_update", virtualTaskVocabularyToolName}
	registrationTurn.Prompt = "그 업무 " + virtualUnregisteredBusiness + " 사업으로 넣어줘"
	registrationTurn.RouterRequiredEvidence = []string{"task_update", virtualTaskVocabularyToolName}
	return VirtualSessionScenario{
		Name:                  name,
		ArtifactDirectoryPath: artifactDirectoryPath,
		RequesterIsAdmin:      isRequesterAdmin,
		InitialTaskBusinesses: virtualRegisteredBusinesses,
		Skills:                []agentcontract.SkillInstruction{flowTaskSkill()},
		AllowedTools:          append([]string{"conversation_history", "memory_search"}, toolNames...),
		CapabilityToolNames:   toolNames,
		InitialToolNames:      []string{"task_add"},
		Turns: []VirtualTurn{{
			Prompt:                 "분기 보고서 정리 업무 추가해줘",
			RouterRequiredEvidence: []string{"task_add"},
			ActionResponses: []string{
				actionInvokeCapabilityTool("task_add", `{"title":"분기 보고서 정리"}`),
				actionFinishMessage("분기 보고서 정리 업무를 추가했어요.", "obs-001"),
			},
			ExpectedToolCallCounts: map[string]int{"task_add": 1, virtualTaskVocabularyToolName: 0},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}, registrationTurn},
	}
}

func TaskBusinessRegistrationAdminScenario(artifactDirectoryPath string) VirtualSessionScenario {
	wholeList := `{"businesses":[{"name":"노스필드"},{"name":"하버라인"},{"name":"` + virtualUnregisteredBusiness + `"}]}`
	return taskBusinessRegistrationScenario("task_business_registration_admin", artifactDirectoryPath, true, VirtualTurn{
		ReplyTargetID: "virtual-message-001",
		ActionResponses: []string{
			actionInvokeCapabilityTool("task_list", `{}`),
			actionInvokeCapabilityTool(virtualTaskVocabularyToolName, wholeList),
			actionInvokeCapabilityTool("task_update", `{"taskHint":"task-1","business":"`+virtualUnregisteredBusiness+`"}`),
			actionFinishMessage(virtualUnregisteredBusiness+" 사업을 등록하고 업무에 넣었어요.", "obs-002", "obs-003"),
		},
		ExpectedSequence: []string{toolResultEventName(virtualTaskVocabularyToolName), toolResultEventName("task_update")},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolRequestedEventName(virtualTaskVocabularyToolName), BodyFragment: virtualRegisteredBusinesses[0], MinCount: 1},
			{Name: toolRequestedEventName(virtualTaskVocabularyToolName), BodyFragment: virtualRegisteredBusinesses[1], MinCount: 1},
			{Name: toolResultEventName(virtualTaskVocabularyToolName), BodyFragment: `"objectType":"task_vocabulary"`, Count: 1},
			{Name: toolResultEventName("task_update"), BodyFragment: `"business":"` + virtualUnregisteredBusiness + `"`, MinCount: 1},
		},
		ExpectedReplyFragments: []string{virtualUnregisteredBusiness},
		ExpectedTaskStatus:     task.TaskStatusCompleted,
	})
}

func TaskBusinessRegistrationMemberRefusedScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return taskBusinessRegistrationScenario("task_business_registration_member_refused", artifactDirectoryPath, false, VirtualTurn{
		ReplyTargetID: "virtual-message-001",
		ActionResponses: []string{
			actionInvokeCapabilityTool("task_update", `{"taskHint":"task-1","business":"`+virtualUnregisteredBusiness+`"}`),
			actionRefusedMessage(virtualUnregisteredBusiness+"는 등록되지 않은 사업이라 관리자가 먼저 등록해야 해요.", "An administrator has to register the business label."),
		},
		ExpectedEventCounts: []VirtualEventCount{
			{Name: toolResultEventName(virtualTaskVocabularyToolName), BodyFragment: `"objectType":"task_vocabulary"`, Count: 0},
			{Name: toolRequestedEventName("task_update"), MaxCount: 1},
		},
		ExpectedReplyFragments: []string{"관리자"},
		AllowedTaskStatuses:    []task.TaskStatus{task.TaskStatusFailed, task.TaskStatusCompleted},
	})
}
