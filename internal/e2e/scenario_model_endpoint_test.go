//go:build appliance

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type scenarioModelEndpoint struct {
	languageModel model.LanguageModelProvider
	decisionModel model.DecisionModel
}

func (endpoint scenarioModelEndpoint) handler() http.Handler {
	multiplexer := http.NewServeMux()
	multiplexer.HandleFunc("POST /v1/chat/completions", endpoint.serveCompletion)
	multiplexer.HandleFunc("POST /decisions", endpoint.serveDecision)
	return multiplexer
}

type wireCompletionRequest struct {
	Messages   []wireMessage              `json:"messages"`
	Tools      []model.ChatCompletionTool `json:"tools"`
	ToolChoice json.RawMessage            `json:"tool_choice"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (endpoint scenarioModelEndpoint) serveCompletion(writer http.ResponseWriter, request *http.Request) {
	var wireRequest wireCompletionRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&wireRequest); errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusBadRequest)
		return
	}
	schemaName := forcedSchemaName(wireRequest.ToolChoice)
	if schemaName == "" && len(wireRequest.Tools) > 0 {
		http.Error(writer, "the scripted model answers structured requests and plain prompts, and this request is a native tool-calling chat", http.StatusUnprocessableEntity)
		return
	}
	if schemaName == "" {
		endpoint.serveText(writer, request, wireRequest)
		return
	}
	response, errorValue := endpoint.languageModel.GenerateStructuredResponse(request.Context(), model.StructuredResponseRequest{
		Messages: modelMessages(wireRequest.Messages),
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:     schemaName,
			Document: string(offeredSchema(wireRequest.Tools, schemaName)),
		},
	})
	if errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeWireJSON(writer, structuredCompletion(schemaName, response))
}

func (endpoint scenarioModelEndpoint) serveText(writer http.ResponseWriter, request *http.Request, wireRequest wireCompletionRequest) {
	prompt := ""
	if len(wireRequest.Messages) > 0 {
		prompt = wireRequest.Messages[len(wireRequest.Messages)-1].Content
	}
	reply, errorValue := endpoint.languageModel.GenerateResponse(request.Context(), prompt)
	if errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeWireJSON(writer, map[string]any{
		"choices": []map[string]any{{
			"message":       map[string]any{"role": "assistant", "content": reply},
			"finish_reason": "stop",
		}},
	})
}

func (endpoint scenarioModelEndpoint) serveDecision(writer http.ResponseWriter, request *http.Request) {
	var wireRequest struct {
		Model     string                            `json:"model"`
		State     json.RawMessage                   `json:"state"`
		Questions map[string]model.DecisionQuestion `json:"questions"`
	}
	if errorValue := json.NewDecoder(request.Body).Decode(&wireRequest); errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusBadRequest)
		return
	}
	response, errorValue := endpoint.decisionModel.Decide(request.Context(), model.DecisionRequest{
		Model:     wireRequest.Model,
		State:     wireRequest.State,
		Questions: wireRequest.Questions,
	})
	if errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeWireJSON(writer, map[string]any{
		"answers":  wireAnswers(response.Answers),
		"usage":    map[string]any{"input_tokens": response.Usage.PromptTokens, "output_tokens": response.Usage.CompletionTokens, "cost": response.Usage.CostUSD},
		"model":    response.ModelName,
		"provider": response.ProviderName,
	})
}

func wireAnswers(answers map[string]model.DecisionAnswer) map[string]any {
	documents := map[string]any{}
	for questionName, answer := range answers {
		documents[questionName] = map[string]any{
			"choice":        answer.Choice,
			"noul":          answer.Noul,
			"probabilities": answer.Probabilities,
			"confidence":    answer.Confidence,
		}
	}
	return documents
}

func forcedSchemaName(toolChoice json.RawMessage) string {
	var choice struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(toolChoice, &choice) != nil {
		return ""
	}
	return strings.TrimSpace(choice.Function.Name)
}

func offeredSchema(tools []model.ChatCompletionTool, toolName string) json.RawMessage {
	for _, tool := range tools {
		if tool.Function.Name == toolName {
			return tool.Function.Parameters
		}
	}
	return nil
}

func modelMessages(messages []wireMessage) []model.Message {
	converted := make([]model.Message, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, model.Message{Role: message.Role, Content: message.Content})
	}
	return converted
}

func structuredCompletion(schemaName string, response model.StructuredResponse) map[string]any {
	return map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []map[string]any{{
					"id":       "call-" + schemaName,
					"type":     "function",
					"function": map[string]any{"name": schemaName, "arguments": response.Content},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	}
}

func writeWireJSON(writer http.ResponseWriter, document any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(document)
}
