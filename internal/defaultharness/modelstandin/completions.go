//go:build !nobundledharness

package modelstandin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/llmcalls"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type completionRequestDocument struct {
	Model      string                      `json:"model"`
	Messages   []completionMessageDocument `json:"messages"`
	Tools      []model.ChatCompletionTool  `json:"tools"`
	ToolChoice json.RawMessage             `json:"tool_choice"`
}

type completionMessageDocument struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentPartDocument struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (server *Server) serveCompletion(writer http.ResponseWriter, request *http.Request) {
	var document completionRequestDocument
	if errorValue := json.NewDecoder(request.Body).Decode(&document); errorValue != nil {
		http.Error(writer, "a chat completion request is JSON: "+errorValue.Error(), http.StatusBadRequest)
		return
	}
	ask := Ask{
		Kind:             AskKindCompletion,
		Model:            document.Model,
		Authorization:    request.Header.Get("Authorization"),
		OfferedToolNames: offeredToolNames(document.Tools),
		About:            lastUserText(document.Messages),
	}
	if schemaName := forcedFunctionName(document.ToolChoice); schemaName != "" {
		server.answerStructured(writer, request, ask, schemaName, document)
		return
	}
	if len(document.Tools) > 0 {
		server.answerAction(writer, request, ask, document)
		return
	}
	server.refuse(writer, ask, "a completion offering no tools names no schema, so no script can answer it")
}

func (server *Server) answerStructured(writer http.ResponseWriter, request *http.Request, ask Ask, schemaName string, document completionRequestDocument) {
	ask.SchemaName = schemaName
	response, errorValue := server.scriptedLanguageModel().GenerateStructuredResponse(request.Context(), model.StructuredResponseRequest{
		Messages: plainMessages(document.Messages),
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:     schemaName,
			Document: string(offeredParameters(document.Tools, schemaName)),
		},
	})
	if errorValue != nil {
		server.refuse(writer, ask, errorValue.Error())
		return
	}
	ask.AnsweredWith = schemaName
	server.record(ask)
	writeJSON(writer, toolCallCompletion(schemaName, response.Content))
}

func (server *Server) answerAction(writer http.ResponseWriter, request *http.Request, ask Ask, document completionRequestDocument) {
	ask.SchemaName = llmcalls.AgentActionSchemaName
	response, errorValue := server.scriptedLanguageModel().ChatCompleter().GenerateChatCompletion(request.Context(), model.ChatCompletionRequest{
		SchemaName: llmcalls.AgentActionSchemaName,
		Messages:   chatMessages(document.Messages),
		Tools:      document.Tools,
		ToolChoice: document.ToolChoice,
	})
	if errorValue != nil {
		server.refuse(writer, ask, errorValue.Error())
		return
	}
	toolCall := response.Message.ToolCalls[0]
	ask.AnsweredWith = toolCall.Function.Name
	server.record(ask)
	writeJSON(writer, toolCallCompletion(toolCall.Function.Name, toolCall.Function.Arguments))
}

const aboutLength = 240

func lastUserText(messages []completionMessageDocument) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			return truncated(messageText(messages[index].Content))
		}
	}
	return ""
}

func truncated(text string) string {
	runes := []rune(text)
	if len(runes) <= aboutLength {
		return text
	}
	return string(runes[:aboutLength]) + "…"
}

func forcedFunctionName(toolChoice json.RawMessage) string {
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

func offeredToolNames(tools []model.ChatCompletionTool) []string {
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		toolNames = append(toolNames, tool.Function.Name)
	}
	return toolNames
}

func offeredParameters(tools []model.ChatCompletionTool, toolName string) json.RawMessage {
	for _, tool := range tools {
		if tool.Function.Name == toolName {
			return tool.Function.Parameters
		}
	}
	return nil
}

func plainMessages(messages []completionMessageDocument) []model.Message {
	plain := make([]model.Message, 0, len(messages))
	for _, message := range messages {
		plain = append(plain, model.Message{Role: message.Role, Content: messageText(message.Content)})
	}
	return plain
}

func chatMessages(messages []completionMessageDocument) []model.ChatCompletionMessage {
	chat := make([]model.ChatCompletionMessage, 0, len(messages))
	for _, message := range messages {
		chat = append(chat, model.ChatCompletionMessage{Role: message.Role, Content: messageText(message.Content)})
	}
	return chat
}

func messageText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []contentPartDocument
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	texts := []string{}
	for _, part := range parts {
		if part.Type == "text" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func toolCallCompletion(toolName string, argumentsDocument string) map[string]any {
	return map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []map[string]any{{
					"id":       "call-" + toolName,
					"type":     "function",
					"function": map[string]any{"name": toolName, "arguments": argumentsDocument},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	}
}
