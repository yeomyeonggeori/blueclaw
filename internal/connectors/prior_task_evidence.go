package connectors

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const priorTaskAttemptLimit = 12
const priorTaskInputByteLimit = 2048

func priorTaskRecordedAttempts(events []task.TaskEvent) ([]agentcontract.PriorTaskAttempt, int) {
	attempts := []agentcontract.PriorTaskAttempt{}
	for _, event := range events {
		toolName := strings.TrimSuffix(strings.TrimPrefix(event.Name, "tool."), agentcontract.ToolTaskEventResultSuffix)
		if event.Name != agentcontract.ToolTaskEventName(toolName, agentcontract.ToolTaskEventResultSuffix) {
			continue
		}
		var attempt agentcontract.PriorTaskAttempt
		if json.Unmarshal([]byte(event.Body), &attempt) != nil || attempt.ObservationID == "" {
			continue
		}
		attempt.Tool = toolName
		if len(attempt.ToolInput) > priorTaskInputByteLimit {
			attempt.ToolInput = nil
			attempt.ToolInputOmitted = true
		}
		attempts = append(attempts, attempt)
	}
	omittedCount := max(0, len(attempts)-priorTaskAttemptLimit)
	return attempts[omittedCount:], omittedCount
}
