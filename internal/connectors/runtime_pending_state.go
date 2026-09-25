package connectors

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type pendingApproval struct {
	TaskRun                 task.TaskRun
	IntentPrompt            string
	ApprovalQuestion        string
	ResponseLanguage        string
	ContinuationInstruction string
	ActiveGoal              agentcontract.ActiveGoal
}

func (connectorRuntime *ConnectorRuntime) findPendingAskInteraction(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (AskInteraction, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findPendingAskInteractionByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID)
	}
	taskRuns := connectorRuntime.taskRunService.ListTaskRunByPersonID(personID)
	var selectedInteraction AskInteraction
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		if taskRun.Status != task.TaskStatusWaitingUserInput {
			continue
		}
		if !taskRunMatchesMessageScope(taskRun, event) {
			continue
		}
		interaction, isFound := latestAskInteraction(taskRun.TaskRunID, connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID))
		if !isFound {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		selectedInteraction = interaction
		isSelected = true
	}
	return selectedInteraction, isSelected
}

func (connectorRuntime *ConnectorRuntime) findPendingAskInteractionByTaskRunID(taskRunID string) (AskInteraction, bool) {
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != task.TaskStatusWaitingUserInput {
		return AskInteraction{}, false
	}
	return latestAskInteraction(taskRun.TaskRunID, connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID))
}

func (connectorRuntime *ConnectorRuntime) findPendingApproval(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (pendingApproval, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findPendingApprovalByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID)
	}
	taskRuns := connectorRuntime.taskRunService.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		if taskRun.Status != task.TaskStatusWaitingApproval {
			continue
		}
		if !taskRunMatchesMessageScope(taskRun, event) {
			continue
		}
		if time.Since(taskRun.UpdatedAt) > 24*time.Hour {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		isSelected = true
	}
	if !isSelected {
		return pendingApproval{}, false
	}
	return connectorRuntime.pendingApprovalForTaskRun(selectedTaskRun), true
}

func (connectorRuntime *ConnectorRuntime) findPendingApprovalByTaskRunID(taskRunID string) (pendingApproval, bool) {
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != task.TaskStatusWaitingApproval {
		return pendingApproval{}, false
	}
	return connectorRuntime.pendingApprovalForTaskRun(taskRun), true
}

func (connectorRuntime *ConnectorRuntime) pendingApprovalForTaskRun(selectedTaskRun task.TaskRun) pendingApproval {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(selectedTaskRun.TaskRunID)
	approvalQuestion := latestApprovalQuestion(taskEvents)
	responseLanguage := latestApprovalResponseLanguage(taskEvents)
	continuationInstruction := latestConfirmationContinuationInstruction(taskEvents)
	activeGoal := latestActiveGoal(taskEvents)
	return pendingApproval{
		TaskRun:                 selectedTaskRun,
		IntentPrompt:            strings.TrimSpace(selectedTaskRun.Prompt),
		ApprovalQuestion:        approvalQuestion,
		ResponseLanguage:        responseLanguage,
		ContinuationInstruction: continuationInstruction,
		ActiveGoal:              activeGoal,
	}
}

func (connectorRuntime *ConnectorRuntime) findActiveGoal(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (agentcontract.ActiveGoal, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findActiveGoalByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID)
	}
	taskRuns := connectorRuntime.taskRunService.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
		if !taskRunCanContinueGoal(taskRun, taskEvents) {
			continue
		}
		if !taskRunMatchesMessageScope(taskRun, event) {
			continue
		}
		if time.Since(taskRun.UpdatedAt) > 24*time.Hour {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		isSelected = true
	}
	if !isSelected {
		return agentcontract.ActiveGoal{}, false
	}
	return connectorRuntime.activeGoalForTaskRun(selectedTaskRun), true
}

func (connectorRuntime *ConnectorRuntime) findActiveGoalByTaskRunID(taskRunID string) (agentcontract.ActiveGoal, bool) {
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return agentcontract.ActiveGoal{}, false
	}
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	if !taskRunCanContinueGoal(taskRun, taskEvents) {
		return agentcontract.ActiveGoal{}, false
	}
	return connectorRuntime.activeGoalForTaskRun(taskRun), true
}

func (connectorRuntime *ConnectorRuntime) findPriorTaskContext(personID string, event PlatformInboundEvent) (agentcontract.PriorTaskContext, bool) {
	taskRuns := connectorRuntime.taskRunService.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	var selectedContext agentcontract.PriorTaskContext
	isSelected := false
	for _, taskRun := range taskRuns {
		if !taskRunCanProvidePriorContext(taskRun) {
			continue
		}
		if taskRun.OriginConversationID != event.ConversationID {
			continue
		}
		if !taskRunMatchesReplyTarget(taskRun, event) {
			continue
		}
		if time.Since(taskRun.UpdatedAt) > 72*time.Hour {
			continue
		}
		taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
		context := priorTaskContextForTaskRun(taskRun, taskEvents)
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		selectedContext = context
		isSelected = true
	}
	return selectedContext, isSelected
}

func (connectorRuntime *ConnectorRuntime) activeGoalForTaskRun(selectedTaskRun task.TaskRun) agentcontract.ActiveGoal {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(selectedTaskRun.TaskRunID)
	activeGoal := latestActiveGoal(taskEvents)
	if strings.TrimSpace(activeGoal.TaskRunID) == "" {
		activeGoal.TaskRunID = selectedTaskRun.TaskRunID
	}
	if strings.TrimSpace(activeGoal.GoalID) == "" {
		activeGoal.GoalID = selectedTaskRun.TaskRunID
	}
	if strings.TrimSpace(activeGoal.OriginalInstruction) == "" {
		activeGoal.OriginalInstruction = selectedTaskRun.Prompt
	}
	if activeGoal.Status == "" {
		activeGoal.Status = activeGoalStatusForTaskRun(selectedTaskRun)
	}
	return activeGoal
}

func taskRunCanProvidePriorContext(taskRun task.TaskRun) bool {
	switch taskRun.Status {
	case task.TaskStatusBlocked, task.TaskStatusFailed, task.TaskStatusCompleted:
		return true
	default:
		return false
	}
}

func taskRunMatchesReplyTarget(taskRun task.TaskRun, event PlatformInboundEvent) bool {
	eventReplyTargetID := strings.TrimSpace(event.ReplyTargetID)
	taskReplyTargetID := strings.TrimSpace(taskRun.OriginReplyTargetID)
	if eventReplyTargetID != "" {
		return taskReplyTargetID == eventReplyTargetID
	}
	return taskReplyTargetID == ""
}

func priorTaskContextForTaskRun(taskRun task.TaskRun, taskEvents []task.TaskEvent) agentcontract.PriorTaskContext {
	activeGoal := latestActiveGoal(taskEvents)
	intakeDecision := latestIntakeDecision(taskEvents)
	requestedOutputFormats := toolcontract.AppendUniqueStrings([]string{}, intakeDecision.RequestedOutputFormats...)
	requestedOutputFormats = toolcontract.AppendUniqueStrings(requestedOutputFormats, outputFormatsFromAttachmentSuffixes(activeGoal.OutcomeContract.RequiredAttachmentSuffixes)...)
	attempts, omittedAttemptCount := priorTaskRecordedAttempts(taskEvents)
	return agentcontract.PriorTaskContext{
		TaskRunID:              strings.TrimSpace(taskRun.TaskRunID),
		Status:                 string(taskRun.Status),
		Prompt:                 strings.TrimSpace(taskRun.Prompt),
		Result:                 strings.TrimSpace(taskRun.Result),
		FailureReason:          strings.TrimSpace(taskRun.FailureReason),
		OutcomeContract:        activeGoal.OutcomeContract,
		RequestedOutputFormats: requestedOutputFormats,
		RecordedAttempts:       attempts,
		OmittedAttemptCount:    omittedAttemptCount,
	}
}

func outputFormatsFromAttachmentSuffixes(suffixes []string) []string {
	formats := []string{}
	for _, suffix := range suffixes {
		format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), ".")
		switch format {
		case "html", "pptx", "pdf", "txt", "docx", "xlsx", "csv":
			formats = toolcontract.AppendUniqueStrings(formats, format)
		}
	}
	return formats
}

func (connectorRuntime *ConnectorRuntime) withPersistedIntakeState(taskRunID string, decision agentcontract.TurnDecision) agentcontract.TurnDecision {
	if decision.Route != agentcontract.TurnRouteContinueTask {
		return decision
	}
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRunID)
	return decision.WithRestoredIntakeState(latestIntakeDecision(taskEvents))
}

func latestIntakeDecision(taskEvents []task.TaskEvent) agentcontract.IntakeDecision {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != agentcontract.TaskEventAgentIntake {
			continue
		}
		var decision agentcontract.IntakeDecision
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &decision); errorValue != nil {
			continue
		}
		return decision
	}
	return agentcontract.IntakeDecision{}
}

func taskRunCanContinueGoal(taskRun task.TaskRun, taskEvents []task.TaskEvent) bool {
	switch taskRun.Status {
	case task.TaskStatusWaitingUserInput, task.TaskStatusWaitingApproval:
		return true
	case task.TaskStatusBlocked:
		return taskRunHasLimitStop(taskEvents) || taskRunHasRecoverableArtifactDelivery(taskEvents)
	default:
		return false
	}
}

func taskRunHasLimitStop(taskEvents []task.TaskEvent) bool {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name == agentcontract.TaskEventAgentLimitStop {
			return true
		}
	}
	return false
}

func taskRunHasRecoverableArtifactDelivery(taskEvents []task.TaskEvent) bool {
	activeGoal := latestActiveGoal(taskEvents)
	return outcomeContractRequiresFileAttachment(activeGoal.OutcomeContract)
}

func outcomeContractRequiresFileAttachment(contract agentcontract.OutcomeContract) bool {
	if len(contract.RequiredAttachmentSuffixes) > 0 {
		return true
	}
	if toolNamesContain(contract.RequiredEvidenceTools, toolcontract.FileDeliverToolName) {
		return true
	}
	if toolNameGroupsContain(contract.RequiredEvidenceAnyOf, toolcontract.FileDeliverToolName) {
		return true
	}
	for _, result := range contract.ExpectedResults {
		if result.Required && result.Type == agentcontract.ExpectedResultTypeFile {
			return true
		}
	}
	return contract.ArtifactRequirement == agentcontract.ArtifactRequirementRequired && agentcontract.OutcomeContractHasRequirements(contract)
}

func toolNamesContain(toolNames []string, expectedToolName string) bool {
	for _, toolName := range toolNames {
		if strings.TrimSpace(toolName) == expectedToolName {
			return true
		}
	}
	return false
}

func toolNameGroupsContain(toolNameGroups [][]string, expectedToolName string) bool {
	for _, toolNameGroup := range toolNameGroups {
		if toolNamesContain(toolNameGroup, expectedToolName) {
			return true
		}
	}
	return false
}

func latestActiveGoal(taskEvents []task.TaskEvent) agentcontract.ActiveGoal {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if !strings.HasPrefix(taskEvent.Name, agentcontract.AgentGoalTaskEventPrefix) {
			continue
		}
		var activeGoal agentcontract.ActiveGoal
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &activeGoal); errorValue != nil {
			return agentcontract.ActiveGoal{RestoreError: "latest active goal event is invalid: " + errorValue.Error()}
		}
		return activeGoal
	}
	return activeGoalFromConfirmationPlan(taskEvents)
}

func activeGoalFromConfirmationPlan(taskEvents []task.TaskEvent) agentcontract.ActiveGoal {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != agentcontract.TaskEventConfirmationPlanCreated {
			continue
		}
		var executionPlan agentcontract.ExecutionPlan
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &executionPlan); errorValue != nil {
			continue
		}
		return agentcontract.ActiveGoal{
			OriginalInstruction: strings.TrimSpace(executionPlan.OriginalInstruction),
			CurrentObjective:    strings.TrimSpace(executionPlan.Summary),
			MissingInformation:  append([]string{}, executionPlan.MissingInformation...),
			Status:              agentcontract.ActiveGoalStatusWaitingUserInput,
		}
	}
	return agentcontract.ActiveGoal{}
}

func activeGoalStatusForTaskRun(taskRun task.TaskRun) agentcontract.ActiveGoalStatus {
	switch taskRun.Status {
	case task.TaskStatusWaitingApproval:
		return agentcontract.ActiveGoalStatusWaitingApproval
	case task.TaskStatusWaitingUserInput:
		return agentcontract.ActiveGoalStatusWaitingUserInput
	case task.TaskStatusBlocked:
		return agentcontract.ActiveGoalStatusBlocked
	default:
		return agentcontract.ActiveGoalStatusActive
	}
}

func approvedContinuationEvent(event PlatformInboundEvent, approval pendingApproval) PlatformInboundEvent {
	event.ResponseLanguage = toolcontract.ResolveResponseLanguage(event.ResponseLanguage, approval.ResponseLanguage)
	return event
}

func pendingApprovalActiveGoal(approval pendingApproval, approvalReply string) agentcontract.ActiveGoal {
	activeGoal := approval.ActiveGoal
	activeGoal.GoalID = firstNonEmptyString(activeGoal.GoalID, approval.TaskRun.TaskRunID)
	activeGoal.TaskRunID = firstNonEmptyString(activeGoal.TaskRunID, approval.TaskRun.TaskRunID)
	activeGoal.OriginalInstruction = firstNonEmptyString(activeGoal.OriginalInstruction, approval.IntentPrompt)
	approvedAction := firstNonEmptyString(activeGoal.CurrentObjective, approval.ContinuationInstruction, approval.IntentPrompt)
	executionDirective := "The user already approved this action; perform it now and do not call ask_confirm again."
	activeGoal.CurrentObjective = strings.TrimSpace(approvedAction + " " + executionDirective)
	activeGoal.KnownContext = append(activeGoal.KnownContext, "The user approved the pending action in the latest message: "+strings.TrimSpace(approvalReply))
	activeGoal.Status = agentcontract.ActiveGoalStatusActive
	return activeGoal
}

func activeGoalForLaunch(activeGoal agentcontract.ActiveGoal, hasActiveGoal bool) agentcontract.ActiveGoal {
	if !hasActiveGoal {
		return agentcontract.ActiveGoal{}
	}
	return activeGoal
}

func pendingConfirmationTaskRunID(approval pendingApproval, isApprovalContinuation bool) string {
	if !isApprovalContinuation {
		return ""
	}
	return strings.TrimSpace(approval.TaskRun.TaskRunID)
}

func existingGoalTaskRunID(approval pendingApproval, isApprovalContinuation bool, activeGoal agentcontract.ActiveGoal, hasActiveGoal bool) string {
	if isApprovalContinuation {
		return pendingConfirmationTaskRunID(approval, true)
	}
	if !hasActiveGoal {
		return ""
	}
	return strings.TrimSpace(activeGoal.TaskRunID)
}
