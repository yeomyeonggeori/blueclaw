package connectors

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

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
		if !taskRunSharesMessageThread(taskRun, event) {
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

func (connectorRuntime *ConnectorRuntime) findActiveGoal(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (agentcontract.ActiveGoal, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findActiveGoalByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID, event)
	}
	taskRuns := connectorRuntime.taskRunService.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
		if !eventCanContinueGoal(event, taskRun, taskEvents) || connectorRuntime.isAwaitedInThread(taskRun.TaskRunID) {
			continue
		}
		if !taskRunSharesMessageThread(taskRun, event) {
			continue
		}
		if time.Since(taskRun.UpdatedAt) > approvalExpiry {
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

func (connectorRuntime *ConnectorRuntime) findActiveGoalByTaskRunID(taskRunID string, event PlatformInboundEvent) (agentcontract.ActiveGoal, bool) {
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return agentcontract.ActiveGoal{}, false
	}
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	if !eventCanContinueGoal(event, taskRun, taskEvents) || connectorRuntime.isAwaitedInThread(taskRun.TaskRunID) {
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
		if !taskRunSharesMessageThread(taskRun, event) {
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

func eventCanContinueGoal(event PlatformInboundEvent, taskRun task.TaskRun, taskEvents []task.TaskEvent) bool {
	return taskRunCanContinueGoal(taskRun, taskEvents)
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
		if taskEvent.Name == agentcontract.TaskEventAgentLimitStop || taskEvent.Name == task.TaskEventLaunchLimitStop {
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

func activeGoalForLaunch(activeGoal agentcontract.ActiveGoal, hasActiveGoal bool) agentcontract.ActiveGoal {
	if !hasActiveGoal {
		return agentcontract.ActiveGoal{}
	}
	return activeGoal
}
