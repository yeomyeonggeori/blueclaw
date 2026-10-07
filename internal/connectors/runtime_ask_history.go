package connectors

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func latestApprovalResponseLanguage(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != agentcontract.TaskEventConfirmationRequested {
			continue
		}
		var approvalRequest struct {
			ResponseLanguage string `json:"responseLanguage"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &approvalRequest); errorValue != nil {
			continue
		}
		if responseLanguage := toolcontract.NormalizeResponseLanguage(approvalRequest.ResponseLanguage); responseLanguage != "" {
			return responseLanguage
		}
	}
	return ""
}

func latestAskInteraction(taskRunID string, taskEvents []task.TaskEvent) (AskInteraction, bool) {
	resolvedInteractionIDs := map[string]bool{}
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name == agentcontract.TaskEventAskResolved {
			interactionID := askResolvedInteractionID(taskEvent)
			if interactionID != "" {
				resolvedInteractionIDs[interactionID] = true
			}
			continue
		}
		if !task.IsAskRequestedEvent(taskEvent.Name) {
			continue
		}
		var interaction AskInteraction
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &interaction); errorValue != nil {
			continue
		}
		interaction.TaskRunID = firstNonEmptyString(interaction.TaskRunID, taskRunID)
		interaction.InteractionID = firstNonEmptyString(interaction.InteractionID, taskEvent.TaskEventID)
		if resolvedInteractionIDs[strings.TrimSpace(interaction.InteractionID)] {
			continue
		}
		if strings.TrimSpace(interaction.Kind) != "" {
			return interaction, true
		}
	}
	return AskInteraction{}, false
}

func askResolvedInteractionID(taskEvent task.TaskEvent) string {
	var resolution struct {
		InteractionID string `json:"interactionID"`
	}
	if errorValue := json.Unmarshal([]byte(taskEvent.Body), &resolution); errorValue != nil {
		return ""
	}
	return strings.TrimSpace(resolution.InteractionID)
}
