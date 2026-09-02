package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type TaskRunHandler struct {
	TaskLauncher            *agentruntime.TaskLauncher
	IdentityService         *identity.IdentityService
	WorkspaceID             string
	TaskRunService          *task.TaskRunService
	TaskIntakeGate          TaskIntakeGate
	AllowTaskDecisionPreset bool
}

type taskRunRequest struct {
	RequesterPersonID    string   `json:"requesterPersonID"`
	RequesterName        string   `json:"requesterName"`
	RequesterCallingName string   `json:"requesterCallingName"`
	RequesterHandle      string   `json:"requesterHandle"`
	ConversationID       string   `json:"conversationID"`
	ProfileName          string   `json:"profileName"`
	Prompt               string   `json:"prompt"`
	PinnedToolNames      []string `json:"pinnedToolNames"`
	PinnedSkillNames     []string `json:"pinnedSkillNames"`
	TaskDecisionPreset   string   `json:"taskDecisionPreset"`
}

const (
	modelPathTaskDecisionPreset    = "model_path"
	modelPathDiagnosticProfileName = "model-path-diagnostic"
)

type taskRunCancelRequest struct {
	TaskRunIDs                 []string `json:"taskRunIDs"`
	RequesterPersonID          string   `json:"requesterPersonID"`
	RequesterEmail             string   `json:"requesterEmail"`
	OriginConversationIDs      []string `json:"originConversationIDs"`
	OriginConversationIDPrefix string   `json:"originConversationIDPrefix"`
	Mode                       string   `json:"mode"`
	ScheduleOnly               bool     `json:"scheduleOnly"`
	StaleBefore                string   `json:"staleBefore"`
	Reason                     string   `json:"reason"`
}

func (taskRunHandler TaskRunHandler) HandleRunTask(responseWriter http.ResponseWriter, request *http.Request) {
	if taskRunHandler.TaskLauncher == nil || taskRunHandler.IdentityService == nil {
		http.Error(responseWriter, "task launcher is not configured", http.StatusServiceUnavailable)
		return
	}
	if taskRunHandler.TaskIntakeGate != nil && taskRunHandler.TaskIntakeGate.IsQuiesced() {
		http.Error(responseWriter, "task intake is quiesced", http.StatusServiceUnavailable)
		return
	}
	var runRequest taskRunRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&runRequest); errorValue != nil {
		http.Error(responseWriter, "invalid task run request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(runRequest.RequesterPersonID) == "" {
		http.Error(responseWriter, "requesterPersonID is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(runRequest.Prompt) == "" {
		http.Error(responseWriter, "prompt is required", http.StatusBadRequest)
		return
	}
	precomputedTurnDecision, statusCode, errorValue := taskRunHandler.resolveTaskDecisionPreset(runRequest.TaskDecisionPreset)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), statusCode)
		return
	}
	if precomputedTurnDecision != nil {
		if hasTaskDecisionPresetOverrides(runRequest) {
			http.Error(responseWriter, "task decision preset does not accept profile, tool, or skill overrides", http.StatusBadRequest)
			return
		}
		runRequest.ProfileName = modelPathDiagnosticProfileName
	}
	personAccess := taskRunHandler.IdentityService.ResolvePersonAccess(runRequest.RequesterPersonID)
	conversationID := firstNonEmptyAdminString(runRequest.ConversationID, "admin:"+runRequest.RequesterPersonID)
	launchResult, errorValue := taskRunHandler.TaskLauncher.Launch(context.Background(), agentruntime.TaskLaunchRequest{
		Source:                     agentruntime.TaskLaunchSourceAdmin,
		SourceReference:            "admin:" + runRequest.RequesterPersonID,
		RequesterPersonID:          runRequest.RequesterPersonID,
		RequesterName:              runRequest.RequesterName,
		RequesterCallingName:       runRequest.RequesterCallingName,
		RequesterHandle:            runRequest.RequesterHandle,
		RequesterEmail:             taskRunHandler.IdentityService.ResolvePersonPrimaryEmail(runRequest.RequesterPersonID),
		ProfileName:                runRequest.ProfileName,
		ConversationID:             conversationID,
		Prompt:                     runRequest.Prompt,
		PinnedToolNames:            append([]string{}, runRequest.PinnedToolNames...),
		PinnedSkillNames:           append([]string{}, runRequest.PinnedSkillNames...),
		PrecomputedTurnDecision:    precomputedTurnDecision,
		IsPrecomputedDecisionExact: precomputedTurnDecision != nil,
		SkipSkillSelection:         precomputedTurnDecision != nil,
		UseEmptyToolCatalog:        precomputedTurnDecision != nil,
		PersonAccess:               personAccess,
		MemoryLabel:                memory.SecurityLabel{SecurityLevelRank: personAccess.SecurityLevelRank, RequiredClasses: append([]string{}, personAccess.GrantedClasses...)},
		AccessibleConversationIDs:  []string{conversationID},
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"taskRun":       launchResult.TurnResult.TaskRun,
		"finishMessage": launchResult.TurnResult.FinishMessage,
		"attachments":   launchResult.TurnResult.Attachments,
	})
}

func hasTaskDecisionPresetOverrides(runRequest taskRunRequest) bool {
	return strings.TrimSpace(runRequest.ProfileName) != "" ||
		len(runRequest.PinnedToolNames) > 0 ||
		len(runRequest.PinnedSkillNames) > 0
}

func (taskRunHandler TaskRunHandler) resolveTaskDecisionPreset(preset string) (*agentcontract.TurnDecision, int, error) {
	normalizedPreset := strings.TrimSpace(preset)
	if normalizedPreset == "" {
		return nil, 0, nil
	}
	if !taskRunHandler.AllowTaskDecisionPreset {
		return nil, http.StatusForbidden, errors.New("task decision presets are disabled")
	}
	if normalizedPreset != modelPathTaskDecisionPreset {
		return nil, http.StatusBadRequest, errors.New("task decision preset is unsupported")
	}
	return &agentcontract.TurnDecision{
		Route:              agentcontract.TurnRouteStartTask,
		Classification:     agentcontract.IntakeClassificationQuickReply,
		TaskShape:          agentcontract.TaskShapeImmediateReply,
		TaskLevel:          agentcontract.TaskLevelXLow,
		PriorTaskReference: agentcontract.PriorTaskReferenceNone,
		Reason:             "model path diagnostic",
	}, 0, nil
}

func (taskRunHandler TaskRunHandler) HandleCancelTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	if taskRunHandler.TaskRunService == nil {
		http.Error(responseWriter, "task run service is not configured", http.StatusServiceUnavailable)
		return
	}
	var cancelRequest taskRunCancelRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&cancelRequest); errorValue != nil {
		http.Error(responseWriter, "invalid task run cancel request", http.StatusBadRequest)
		return
	}
	staleBefore, errorValue := parseOptionalAdminTime(cancelRequest.StaleBefore)
	if errorValue != nil {
		http.Error(responseWriter, "staleBefore must be RFC3339", http.StatusBadRequest)
		return
	}
	requesterPersonID, errorValue := taskRunHandler.resolveCancelRequesterPersonID(cancelRequest)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(cancelRequest.Mode) == "stop" || strings.TrimSpace(cancelRequest.Mode) == "stop_all" {
		if strings.TrimSpace(requesterPersonID) == "" {
			http.Error(responseWriter, "requesterPersonID or requesterEmail is required for stop", http.StatusBadRequest)
			return
		}
		taskRunHandler.handleStopTaskRun(responseWriter, cancelRequest, requesterPersonID, staleBefore)
		return
	}
	if !hasTaskRunCancelSelector(cancelRequest) {
		http.Error(responseWriter, "taskRunIDs, requesterPersonID, or scheduleOnly is required", http.StatusBadRequest)
		return
	}
	taskRunCancelRequest := task.TaskRunCancelRequest{
		TaskRunIDs:                 cancelRequest.TaskRunIDs,
		RequesterPersonID:          requesterPersonID,
		OriginConversationIDs:      cancelRequest.OriginConversationIDs,
		ScheduleOnly:               cancelRequest.ScheduleOnly,
		StaleBefore:                staleBefore,
		Reason:                     firstNonEmptyAdminString(cancelRequest.Reason, "admin task cancel"),
		OriginConversationIDPrefix: firstNonEmptyAdminString(cancelRequest.OriginConversationIDPrefix, adminScheduleOriginPrefix(cancelRequest.ScheduleOnly)),
	}
	cancelledTaskRuns := taskRunHandler.TaskRunService.CancelActiveTaskRuns(taskRunCancelRequest)
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"cancelledTaskRunCount": len(cancelledTaskRuns),
		"taskRuns":              cancelledTaskRuns,
		"scheduleTouched":       false,
	})
}

func hasTaskRunCancelSelector(request taskRunCancelRequest) bool {
	return len(request.TaskRunIDs) > 0 || strings.TrimSpace(request.RequesterPersonID) != "" || strings.TrimSpace(request.RequesterEmail) != "" || request.ScheduleOnly || len(request.OriginConversationIDs) > 0
}

func (taskRunHandler TaskRunHandler) handleStopTaskRun(responseWriter http.ResponseWriter, cancelRequest taskRunCancelRequest, requesterPersonID string, staleBefore *time.Time) {
	reason := firstNonEmptyAdminString(cancelRequest.Reason, "user requested task stop")
	mode := strings.TrimSpace(cancelRequest.Mode)
	cancelledTaskRuns := []task.TaskRun{}
	hasMultipleTargets := false
	if mode == "stop_all" {
		cancelledTaskRuns = taskRunHandler.TaskRunService.CancelActiveTaskRuns(task.TaskRunCancelRequest{
			RequesterPersonID: requesterPersonID,
			StaleBefore:       staleBefore,
			Reason:            reason,
		})
	} else {
		if len(cancelRequest.OriginConversationIDs) > 0 {
			cancelledTaskRuns = taskRunHandler.TaskRunService.CancelActiveTaskRuns(task.TaskRunCancelRequest{
				RequesterPersonID:     requesterPersonID,
				OriginConversationIDs: cancelRequest.OriginConversationIDs,
				StaleBefore:           staleBefore,
				Reason:                reason,
			})
		}
		if len(cancelledTaskRuns) == 0 {
			activeTaskRuns := taskRunHandler.activeTaskRunsForPerson(requesterPersonID)
			if len(activeTaskRuns) == 1 {
				cancelledTaskRuns = taskRunHandler.TaskRunService.CancelActiveTaskRuns(task.TaskRunCancelRequest{
					TaskRunIDs:        []string{activeTaskRuns[0].TaskRunID},
					RequesterPersonID: requesterPersonID,
					StaleBefore:       staleBefore,
					Reason:            reason,
				})
			} else if len(activeTaskRuns) > 1 {
				hasMultipleTargets = true
			}
		}
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"cancelledTaskRunCount": len(cancelledTaskRuns),
		"taskRuns":              cancelledTaskRuns,
		"multipleTargets":       hasMultipleTargets,
		"reason":                reason,
		"scheduleTouched":       false,
	})
}

func (taskRunHandler TaskRunHandler) resolveCancelRequesterPersonID(cancelRequest taskRunCancelRequest) (string, error) {
	requesterPersonID := strings.TrimSpace(cancelRequest.RequesterPersonID)
	if requesterPersonID != "" {
		return requesterPersonID, nil
	}
	requesterEmail := strings.TrimSpace(cancelRequest.RequesterEmail)
	if requesterEmail == "" {
		return "", nil
	}
	if taskRunHandler.IdentityService == nil {
		return "", errors.New("identity service is not configured")
	}
	personID, isFound := taskRunHandler.IdentityService.ResolvePersonIDByEmail(requesterEmail)
	if !isFound {
		return "", errors.New("requester email is not approved")
	}
	return personID, nil
}

func (taskRunHandler TaskRunHandler) activeTaskRunsForPerson(personID string) []task.TaskRun {
	taskRuns := []task.TaskRun{}
	for _, taskRun := range taskRunHandler.TaskRunService.ListTaskRunByPersonID(personID) {
		if isActiveTaskRunStatus(taskRun.Status) {
			taskRuns = append(taskRuns, taskRun)
		}
	}
	return taskRuns
}

func isActiveTaskRunStatus(status task.TaskStatus) bool {
	switch status {
	case task.TaskStatusPlanned, task.TaskStatusRunning, task.TaskStatusWaitingApproval, task.TaskStatusWaitingUserInput, task.TaskStatusBlocked:
		return true
	default:
		return false
	}
}

func parseOptionalAdminTime(value string) (*time.Time, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil, nil
	}
	parsedTime, errorValue := time.Parse(time.RFC3339, trimmedValue)
	if errorValue != nil {
		return nil, errorValue
	}
	return &parsedTime, nil
}

func adminScheduleOriginPrefix(scheduleOnly bool) string {
	if scheduleOnly {
		return "schedule:"
	}
	return ""
}

func firstNonEmptyAdminString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
