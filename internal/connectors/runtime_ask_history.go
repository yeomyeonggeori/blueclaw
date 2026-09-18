package connectors

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func latestApprovalQuestion(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != agentcontract.TaskEventConfirmationRequested {
			continue
		}
		var approvalRequest struct {
			UserFacingMessage string `json:"userFacingMessage"`
			Message           string `json:"message"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &approvalRequest); errorValue != nil {
			continue
		}
		question := firstNonEmptyString(approvalRequest.UserFacingMessage, approvalRequest.Message)
		if strings.TrimSpace(question) != "" {
			return strings.TrimSpace(question)
		}
	}
	return ""
}

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
		if taskEvent.Name != agentcontract.TaskEventAskRequested {
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

func latestAskInteractionID(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name == agentcontract.TaskEventAskRequested {
			return strings.TrimSpace(taskEvent.TaskEventID)
		}
	}
	return ""
}

func latestAskPromptDispatchID(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != agentcontract.TaskEventConnectorReplySent {
			continue
		}
		var replyEvent struct {
			ReplyKind  string `json:"replyKind"`
			DispatchID string `json:"dispatchID"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &replyEvent); errorValue != nil {
			continue
		}
		if strings.TrimSpace(replyEvent.ReplyKind) == connectorReplyKindUserNotice && strings.TrimSpace(replyEvent.DispatchID) != "" {
			return strings.TrimSpace(replyEvent.DispatchID)
		}
	}
	return ""
}

func legacyString(fields map[string]interface{}, key string) string {
	value, _ := fields[key].(string)
	return strings.TrimSpace(value)
}

func legacyBool(fields map[string]interface{}, key string) bool {
	value, _ := fields[key].(bool)
	return value
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

func latestConfirmationContinuationInstruction(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != agentcontract.TaskEventConfirmationRequested {
			continue
		}
		var request struct {
			ContinuationInstruction string `json:"continuationInstruction"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &request); errorValue != nil {
			continue
		}
		if instruction := strings.TrimSpace(request.ContinuationInstruction); instruction != "" {
			return instruction
		}
	}
	return ""
}
