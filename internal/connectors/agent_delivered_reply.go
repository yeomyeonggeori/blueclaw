package connectors

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const messageSendToolName = "message_send"

type toolRequestedEventBody struct {
	ToolName string          `json:"toolName"`
	Input    json.RawMessage `json:"input"`
}

func agentAlreadyDeliveredTo(taskEvents []agentcontract.TaskEvent, deliveryTargets []string) bool {
	knownTargets := []string{}
	for _, deliveryTarget := range deliveryTargets {
		if trimmedTarget := strings.TrimSpace(deliveryTarget); trimmedTarget != "" {
			knownTargets = append(knownTargets, trimmedTarget)
		}
	}
	if len(knownTargets) == 0 {
		return false
	}
	if !messageSendSucceeded(taskEvents) {
		return false
	}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != agentcontract.ToolTaskEventName(messageSendToolName, agentcontract.ToolTaskEventRequestedSuffix) {
			continue
		}
		requested := toolRequestedEventBody{}
		if json.Unmarshal([]byte(taskEvent.Body), &requested) != nil {
			continue
		}
		if messageSendTargets(requested.Input, knownTargets) {
			return true
		}
	}
	return false
}

func messageSendTargets(toolInput json.RawMessage, knownTargets []string) bool {
	decodedInput := map[string]any{}
	if json.Unmarshal(toolInput, &decodedInput) != nil {
		return false
	}
	for _, fieldName := range []string{"conversationID", "targetID", "channelID", "threadID", "replyTargetID"} {
		value, isText := decodedInput[fieldName].(string)
		if !isText {
			continue
		}
		for _, knownTarget := range knownTargets {
			if strings.TrimSpace(value) == knownTarget {
				return true
			}
		}
	}
	return false
}

func messageSendSucceeded(taskEvents []agentcontract.TaskEvent) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != agentcontract.ToolTaskEventName(messageSendToolName, agentcontract.ToolTaskEventResultSuffix) {
			continue
		}
		if !strings.Contains(taskEvent.Body, "\"failure\"") {
			return true
		}
	}
	return false
}
