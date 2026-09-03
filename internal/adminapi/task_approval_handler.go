package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type TaskApprovalHandler struct {
	TaskLauncher    *agentruntime.TaskLauncher
	TaskRunService  *task.TaskRunService
	IdentityService *identity.IdentityService
}

type taskApprovalRequest struct {
	TaskRunID string `json:"taskRunID"`
	Decision  string `json:"decision"`
}

type taskApprovalResponse struct {
	TaskRunID string `json:"taskRunID"`
	Status    string `json:"status"`
}

func (handler TaskApprovalHandler) HandleApproveTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	if handler.TaskRunService == nil || handler.TaskLauncher == nil {
		writeApprovalError(responseWriter, http.StatusServiceUnavailable, "task approval is not configured")
		return
	}
	var approvalRequest taskApprovalRequest
	if json.NewDecoder(request.Body).Decode(&approvalRequest) != nil {
		writeApprovalError(responseWriter, http.StatusBadRequest, "invalid task approval request")
		return
	}
	taskRun, turnDecision, errorValue := handler.resolveApproval(approvalRequest)
	if errorValue != nil {
		writeApprovalError(responseWriter, http.StatusBadRequest, errorValue.Error())
		return
	}
	if turnDecision.Approval != nil && *turnDecision.Approval == agentcontract.ApprovalSignalApproveTask {
		handler.grantApprovalScope(taskRun.TaskRunID)
	}
	approvalgate.RecordRequesterDecision(handler.TaskRunService, taskRun.TaskRunID, turnDecision.Approval, "operator_terminal")
	launchResult, errorValue := handler.TaskLauncher.Launch(context.Background(), agentruntime.TaskLaunchRequest{
		Source:                     agentruntime.TaskLaunchSourceAdmin,
		SourceReference:            "terminal:" + taskRun.TaskRunID,
		RequesterPersonID:          taskRun.RequesterPersonID,
		RequesterEmail:             handler.requesterEmail(taskRun.RequesterPersonID),
		IsApprovalContinuation:     true,
		ExistingTaskRunID:          taskRun.TaskRunID,
		ConversationID:             taskRun.OriginConversationID,
		Prompt:                     taskRun.Prompt,
		PrecomputedTurnDecision:    &turnDecision,
		IsPrecomputedDecisionExact: true,
		PersonAccess:               handler.personAccess(taskRun.RequesterPersonID),
	})
	if errorValue != nil {
		writeApprovalError(responseWriter, http.StatusInternalServerError, errorValue.Error())
		return
	}
	writeApprovalResponse(responseWriter, taskApprovalResponse{
		TaskRunID: taskRun.TaskRunID,
		Status:    string(launchResult.TurnResult.TaskRun.Status),
	})
}

func (handler TaskApprovalHandler) resolveApproval(approvalRequest taskApprovalRequest) (task.TaskRun, agentcontract.TurnDecision, error) {
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	if taskRunID == "" {
		return task.TaskRun{}, agentcontract.TurnDecision{}, errors.New("taskRunID is required")
	}
	taskRun, isFound := handler.TaskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return task.TaskRun{}, agentcontract.TurnDecision{}, errors.New("task run not found")
	}
	if taskRun.Status != task.TaskStatusWaitingApproval {
		return task.TaskRun{}, agentcontract.TurnDecision{}, errors.New("task run is not waiting for approval")
	}
	turnDecision, errorValue := approvalTurnDecision(approvalRequest.Decision)
	if errorValue != nil {
		return task.TaskRun{}, agentcontract.TurnDecision{}, errorValue
	}
	return taskRun, turnDecision, nil
}

func approvalTurnDecision(decision string) (agentcontract.TurnDecision, error) {
	switch strings.TrimSpace(decision) {
	case "confirm":
		approvalSignal := agentcontract.ApprovalSignalApprove
		return continueTaskDecision(approvalSignal, "terminal_confirm"), nil
	case "confirm_task":
		approvalSignal := agentcontract.ApprovalSignalApproveTask
		return continueTaskDecision(approvalSignal, "terminal_confirm_task"), nil
	case "cancel":
		approvalSignal := agentcontract.ApprovalSignalReject
		return agentcontract.TurnDecision{
			Route:          agentcontract.TurnRouteConsume,
			Approval:       &approvalSignal,
			Classification: agentcontract.IntakeClassificationQuickReply,
			TaskShape:      agentcontract.TaskShapeImmediateReply,
			TaskLevel:      agentcontract.TaskLevelXLow,
			Reason:         "terminal_cancel",
		}, nil
	default:
		return agentcontract.TurnDecision{}, errors.New(`decision must be one of "confirm", "confirm_task", "cancel"`)
	}
}

func continueTaskDecision(approvalSignal agentcontract.ApprovalSignal, reason string) agentcontract.TurnDecision {
	return agentcontract.TurnDecision{
		Route:          agentcontract.TurnRouteContinueTask,
		Approval:       &approvalSignal,
		Classification: agentcontract.IntakeClassificationBoundedTask,
		TaskShape:      agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:      agentcontract.TaskLevelLow,
		Reason:         reason,
	}
}

func (handler TaskApprovalHandler) grantApprovalScope(taskRunID string) {
	scope := pendingApprovalScopeForTaskRun(handler.TaskRunService.ListTaskEvent(taskRunID))
	if scope == "" {
		return
	}
	handler.TaskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalScopeGranted, marshalApprovalEventBody(map[string]string{"scope": scope}))
}

func pendingApprovalScopeForTaskRun(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if taskEvents[index].Name != agentcontract.TaskEventAskRequested {
			continue
		}
		var askBody struct {
			ApprovalScope string `json:"approvalScope"`
		}
		if json.Unmarshal([]byte(taskEvents[index].Body), &askBody) == nil {
			return strings.TrimSpace(askBody.ApprovalScope)
		}
	}
	return ""
}

func (handler TaskApprovalHandler) personAccess(personID string) policy.PersonAccess {
	if handler.IdentityService == nil {
		return policy.PersonAccess{PersonID: personID}
	}
	return handler.IdentityService.ResolvePersonAccess(personID)
}

func (handler TaskApprovalHandler) requesterEmail(personID string) string {
	if handler.IdentityService == nil {
		return ""
	}
	return handler.IdentityService.ResolvePersonPrimaryEmail(personID)
}

func marshalApprovalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func writeApprovalError(responseWriter http.ResponseWriter, statusCode int, message string) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	_ = json.NewEncoder(responseWriter).Encode(map[string]string{"error": message})
}

func writeApprovalResponse(responseWriter http.ResponseWriter, response taskApprovalResponse) {
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(response)
}
