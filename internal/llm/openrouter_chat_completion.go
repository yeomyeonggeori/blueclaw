package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type openRouterToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (client OpenRouterClient) GenerateChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	requestDocument := map[string]any{
		"model":    client.modelName(),
		"messages": openRouterChatMessages(request.Messages),
		"stream":   false,
	}
	if len(request.Tools) > 0 {
		requestDocument["tools"] = request.Tools
		requestDocument["parallel_tool_calls"] = request.ParallelToolCalls
	}
	if len(request.ToolChoice) > 0 {
		requestDocument["tool_choice"] = json.RawMessage(request.ToolChoice)
	}
	if request.GenerationOptions.MaxTokens != nil {
		requestDocument["max_tokens"] = *request.GenerationOptions.MaxTokens
	}
	responseDocument, errorValue := client.postChatCompletion(responseContext, requestDocument)
	if errorValue != nil {
		return ChatCompletionResponse{}, errorValue
	}
	return openRouterChatCompletionResponse(responseDocument, client.modelName())
}

func (client OpenRouterClient) GenerateRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	return client.GenerateChatCompletion(responseContext, request)
}

func openRouterChatMessages(messages []ChatCompletionMessage) []map[string]any {
	documents := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		document := map[string]any{"role": message.Role, "content": openRouterMessageContent(Message{Role: message.Role, Content: message.Content, Parts: message.Parts})}
		if strings.TrimSpace(message.ToolCallID) != "" {
			document["tool_call_id"] = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			document["tool_calls"] = message.ToolCalls
		}
		documents = append(documents, document)
	}
	return documents
}

func openRouterChatCompletionResponse(responseDocument []byte, modelName string) (ChatCompletionResponse, error) {
	var decoded struct {
		Choices []struct {
			Message struct {
				Role      string               `json:"role"`
				Content   string               `json:"content"`
				ToolCalls []openRouterToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage openRouterUsage `json:"usage"`
	}
	if errorValue := json.Unmarshal(responseDocument, &decoded); errorValue != nil {
		return ChatCompletionResponse{}, errorValue
	}
	if len(decoded.Choices) == 0 {
		return ChatCompletionResponse{}, errors.New("openrouter chat completion returned no choices")
	}
	choice := decoded.Choices[0]
	toolCalls := make([]ChatCompletionToolCall, 0, len(choice.Message.ToolCalls))
	for _, toolCall := range choice.Message.ToolCalls {
		converted := ChatCompletionToolCall{ID: toolCall.ID, Type: toolCall.Type}
		converted.Function.Name = toolCall.Function.Name
		converted.Function.Arguments = toolCall.Function.Arguments
		toolCalls = append(toolCalls, converted)
	}
	return ChatCompletionResponse{
		Transport:    "http",
		ProviderName: "openrouter",
		ModelName:    modelName,
		FinishReason: choice.FinishReason,
		Message: ChatCompletionMessage{
			Role:      choice.Message.Role,
			Content:   choice.Message.Content,
			ToolCalls: toolCalls,
		},
		Usage: openRouterChatUsage(decoded.Usage),
	}, nil
}

func openRouterChatUsage(usage openRouterUsage) Usage {
	return Usage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
}

func (client OpenRouterClient) postChatCompletion(responseContext context.Context, requestDocument map[string]any) ([]byte, error) {
	encoded, errorValue := json.Marshal(requestDocument)
	if errorValue != nil {
		return nil, errorValue
	}
	httpRequest, errorValue := http.NewRequestWithContext(responseContext, http.MethodPost, client.baseURL(), bytes.NewReader(encoded))
	if errorValue != nil {
		return nil, errorValue
	}
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(client.APIKey))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, errorValue := client.httpClient().Do(httpRequest)
	if errorValue != nil {
		return nil, errorValue
	}
	defer httpResponse.Body.Close()
	responseDocument, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return nil, errorValue
	}
	if httpResponse.StatusCode >= http.StatusBadRequest {
		return nil, errors.New(openRouterHTTPErrorMessage(httpResponse.StatusCode, responseDocument))
	}
	return responseDocument, nil
}
