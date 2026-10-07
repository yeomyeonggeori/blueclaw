package connectors

import (
	"encoding/json"
	"strconv"
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
		var interaction struct {
			AskInteraction
			Choices []string `json:"choices,omitempty"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &interaction); errorValue != nil {
			continue
		}
		interaction.AskInteraction.TaskRunID = firstNonEmptyString(interaction.TaskRunID, taskRunID)
		interaction.AskInteraction.InteractionID = firstNonEmptyString(interaction.InteractionID, taskEvent.TaskEventID)
		if resolvedInteractionIDs[strings.TrimSpace(interaction.InteractionID)] {
			continue
		}
		legacyKind := strings.TrimSpace(interaction.Kind)
		interaction.AskInteraction.Kind = normalizedAskInteractionKind(legacyKind)
		if len(interaction.Options) == 0 && len(interaction.Choices) > 0 {
			interaction.AskInteraction.Options = askOptionsFromLegacyChoices(interaction.Choices)
		}
		if interaction.Kind == "ask_input" && strings.TrimSpace(interaction.SelectionMode) == "" && len(interaction.Options) > 0 {
			interaction.AskInteraction.SelectionMode = askInputSelectionMode(legacyKind)
		}
		if strings.TrimSpace(interaction.Question) == "" {
			interaction.Question = strings.TrimSpace(interaction.Message)
		}
		if strings.TrimSpace(interaction.Message) == "" {
			interaction.Message = strings.TrimSpace(interaction.Question)
		}
		if strings.TrimSpace(interaction.Kind) != "" {
			return interaction.AskInteraction, true
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

func askOptionsFromLegacyChoices(choices []string) []AskChoiceOption {
	options := []AskChoiceOption{}
	for index, choice := range choices {
		trimmedChoice := strings.TrimSpace(choice)
		if trimmedChoice == "" {
			continue
		}
		options = append(options, AskChoiceOption{
			Key:   strconv.Itoa(index + 1),
			Label: trimmedChoice,
			Value: trimmedChoice,
		})
	}
	return options
}

func askInputSelectionMode(legacyKind string) string {
	if strings.TrimSpace(legacyKind) == "choice_multiple" {
		return "multiple"
	}
	return "single"
}

func normalizedAskInteractionKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "confirm":
		return "ask_confirm"
	case "choice_single":
		return "ask_input"
	case "choice_multiple":
		return "ask_input"
	case "input", "input_choice":
		return "ask_input"
	default:
		return strings.TrimSpace(kind)
	}
}
