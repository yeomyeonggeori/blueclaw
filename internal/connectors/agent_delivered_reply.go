package connectors

import (
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func agentAnsweredTheConversation(taskEvents []agentcontract.TaskEvent, conversation VisibleContext) bool {
	requestedCalls := map[string]agentcontract.ToolCallRequested{}
	for _, taskEvent := range taskEvents {
		if _, isRequest := agentcontract.ToolTaskEventToolName(taskEvent.Name, agentcontract.ToolTaskEventRequestedSuffix); isRequest {
			requested := agentcontract.ToolCallRecordOf[agentcontract.ToolCallRequested](taskEvent.Body)
			requestedCalls[requested.ObservationID] = requested
			continue
		}
		if _, isResult := agentcontract.ToolTaskEventToolName(taskEvent.Name, agentcontract.ToolTaskEventResultSuffix); !isResult {
			continue
		}
		result := agentcontract.ToolCallRecordOf[agentcontract.ToolCallResult](taskEvent.Body)
		if result.Succeeded() && approvalgate.CallLandsInTheConversationBeingAnswered(conversation.ConversationType, conversation.ChannelID, requestedCalls[result.ObservationID]) {
			return true
		}
	}
	return false
}
