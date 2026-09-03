package agentruntime

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

// Anything on the request that reaches a model request has to be reconstructable
// from the task ledger, so a field here is either recorded or declared as one the
// model never sees. A field that is neither fails ModelVisibleFieldsAreClassified,
// which is what makes adding one a decision instead of an omission.
var modelVisibleRequestFields = []string{
	"RequesterPersonID", "RequesterEmail", "RequesterName", "RequesterCallingName",
	"RequesterHandle", "RequesterCircles", "Company", "ConversationID", "ConversationType",
	"Platform", "Prompt", "InputParts", "ResponseLanguage", "VisibleContext", "MemoryFacts",
	"AvailableSkills", "WorkspaceRootPath", "WorkspaceDefaultPath", "WorkspaceGuidance",
	"AgentIdentity", "ActivePaths", "HostInstruction", "InstructionPrompt", "InstructionSources",
	"ContractToolWorkingSet", "RequiredEvidenceTools", "RequiredAttachmentSuffixes",
	"OutcomeContract", "ActiveGoal", "PriorTask", "ScheduledRun", "TaskShape", "TaskLevel",
	"TurnStartedAt", "EnvironmentNow", "CarriedOutCalls", "StepBudgetContext", "ArtifactManifest",
	"PinnedToolNames", "PinnedSkillNames", "SkillQueries",
}

var runtimeOnlyRequestFields = []string{
	"RequesterPlatformUserID", "SourceReference", "IsApprovalContinuation", "IsRuntimeRestartResume",
	"ExistingTaskRunID", "IsTaskRunOpenedForThisTurn", "OriginReplyTargetID", "OriginIsThread",
	"ProfileName", "ToolSet", "SkillDecisions", "SkillRetrievalMode", "SkillIndexStatus",
	"SkillCandidateCount", "ToolExposure", "PrecomputedTurnDecision", "IsPrecomputedDecisionExact",
	"SkipSkillSelection", "EffortStartedAt", "TurnAnchorClamped", "OriginalTurnStartedAt",
	"CheckpointSender", "RestrictActionToTerminalOnly",
}

func modelVisibleContextDocument(request agentcontract.AgentTurnRequest) string {
	document, errorValue := json.Marshal(map[string]any{
		"requesterPersonID":          request.RequesterPersonID,
		"requesterEmail":             request.RequesterEmail,
		"requesterName":              request.RequesterName,
		"requesterCallingName":       request.RequesterCallingName,
		"requesterHandle":            request.RequesterHandle,
		"requesterCircles":           request.RequesterCircles,
		"company":                    request.Company,
		"conversationID":             request.ConversationID,
		"conversationType":           request.ConversationType,
		"platform":                   request.Platform,
		"prompt":                     request.Prompt,
		"inputParts":                 request.InputParts,
		"responseLanguage":           request.ResponseLanguage,
		"visibleContext":             request.VisibleContext,
		"memoryFacts":                request.MemoryFacts,
		"availableSkills":            request.AvailableSkills,
		"workspaceRootPath":          request.WorkspaceRootPath,
		"workspaceDefaultPath":       request.WorkspaceDefaultPath,
		"workspaceGuidance":          request.WorkspaceGuidance,
		"agentIdentity":              request.AgentIdentity,
		"activePaths":                request.ActivePaths,
		"hostInstruction":            request.HostInstruction,
		"instructionPrompt":          request.InstructionPrompt,
		"instructionSources":         request.InstructionSources,
		"contractToolWorkingSet":     request.ContractToolWorkingSet,
		"requiredEvidenceTools":      request.RequiredEvidenceTools,
		"requiredAttachmentSuffixes": request.RequiredAttachmentSuffixes,
		"outcomeContract":            request.OutcomeContract,
		"activeGoal":                 request.ActiveGoal,
		"priorTask":                  request.PriorTask,
		"scheduledRun":               request.ScheduledRun,
		"taskShape":                  request.TaskShape,
		"taskLevel":                  request.TaskLevel,
		"turnStartedAt":              request.TurnStartedAt,
		"environmentNow":             request.EnvironmentNow,
		"carriedOutCalls":            request.CarriedOutCalls,
		"stepBudgetContext":          request.StepBudgetContext,
		"artifactManifest":           request.ArtifactManifest,
		"pinnedToolNames":            request.PinnedToolNames,
		"pinnedSkillNames":           request.PinnedSkillNames,
		"skillQueries":               request.SkillQueries,
	})
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func (taskLauncher *TaskLauncher) recordModelVisibleContext(taskRunID string, request agentcontract.AgentTurnRequest) {
	if strings.TrimSpace(taskRunID) == "" || taskLauncher.toolCatalogBuilder.taskRunService == nil {
		return
	}
	document := modelVisibleContextDocument(request)
	if document == "" {
		return
	}
	taskLauncher.toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventTaskModelVisibleContext, document)
}
