package agenttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type ScriptedLanguageModelOptions struct {
	ActionResponses             []string
	ChatResponsesBySchema       map[string][]string
	StructuredResponsesBySchema map[string][]string
	DefaultResponsesBySchema    map[string]string
	ProviderName                string
	ModelName                   string
}

type ScriptedLanguageModel struct {
	mutex                          sync.Mutex
	actionResponses                []string
	chatResponsesBySchema          map[string][]string
	structuredResponsesBySchema    map[string][]string
	lastStructuredResponseBySchema map[string]string
	defaultResponsesBySchema       map[string]string
	requests                       []model.StructuredResponseRequest
	providerName                   string
	modelName                      string
}

type scriptedChatCompleter struct {
	languageModel *ScriptedLanguageModel
}

func NewScriptedLanguageModel(options ScriptedLanguageModelOptions) *ScriptedLanguageModel {
	return &ScriptedLanguageModel{
		actionResponses:             append([]string{}, options.ActionResponses...),
		chatResponsesBySchema:       copyResponseQueues(options.ChatResponsesBySchema),
		structuredResponsesBySchema: copyResponseQueues(options.StructuredResponsesBySchema),
		defaultResponsesBySchema:    mergeDefaultResponses(options.DefaultResponsesBySchema),
		providerName:                firstNonEmpty(options.ProviderName, "test"),
		modelName:                   firstNonEmpty(options.ModelName, "scripted"),
	}
}

func (languageModel *ScriptedLanguageModel) TextChatCompleter() (model.ChatCompleter, bool) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	if len(languageModel.chatResponsesBySchema) == 0 {
		return nil, false
	}
	return scriptedChatCompleter{languageModel: languageModel}, true
}

func (completer scriptedChatCompleter) GenerateChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	languageModel := completer.languageModel
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	schemaName := strings.TrimSpace(request.SchemaName)
	languageModel.requests = append(languageModel.requests, structuredRequestFromChat(request))
	if schemaName == "bluecollar_agent_turn_action" {
		response, errorValue := languageModel.popActionResponse()
		if errorValue != nil {
			return model.ChatCompletionResponse{}, errorValue
		}
		return languageModel.actionChatResponse(request, response)
	}
	responses := languageModel.chatResponsesBySchema[schemaName]
	if len(responses) == 0 {
		return model.ChatCompletionResponse{}, fmt.Errorf("scripted language model has no %s chat response", request.SchemaName)
	}
	languageModel.chatResponsesBySchema[schemaName] = responses[1:]
	return model.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    languageModel.providerName,
		ModelName:       languageModel.modelName,
		SelectedBackend: "device",
		Message: model.ChatCompletionMessage{
			Role:    "assistant",
			Content: responses[0],
		},
	}, nil
}

func (languageModel *ScriptedLanguageModel) actionChatResponse(request model.ChatCompletionRequest, content string) (model.ChatCompletionResponse, error) {
	var actionDocument struct {
		Action    string          `json:"action"`
		ToolName  string          `json:"toolName"`
		ToolInput json.RawMessage `json:"toolInput"`
	}
	if errorValue := json.Unmarshal([]byte(content), &actionDocument); errorValue != nil {
		return model.ChatCompletionResponse{}, errorValue
	}
	toolName := strings.TrimSpace(actionDocument.Action)
	arguments := json.RawMessage(content)
	if toolName == "continue" {
		toolName = strings.TrimSpace(actionDocument.ToolName)
		arguments = actionDocument.ToolInput
	}
	if toolName == "" || len(arguments) == 0 {
		return model.ChatCompletionResponse{}, errors.New("scripted agent action is incomplete")
	}
	if !chatRequestHasTool(request, toolName) {
		return model.ChatCompletionResponse{}, fmt.Errorf("scripted agent action tool %q is not exposed; available tools: %s", toolName, strings.Join(chatRequestToolNames(request), ", "))
	}
	arguments = removeActionDiscriminator(arguments)
	return languageModel.toolCallResponse(toolName, arguments), nil
}

func chatRequestHasTool(request model.ChatCompletionRequest, toolName string) bool {
	for _, availableToolName := range chatRequestToolNames(request) {
		if availableToolName == toolName {
			return true
		}
	}
	return false
}

func chatRequestToolNames(request model.ChatCompletionRequest) []string {
	toolNames := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		toolNames = append(toolNames, tool.Function.Name)
	}
	return toolNames
}

func (languageModel *ScriptedLanguageModel) toolCallResponse(toolName string, arguments json.RawMessage) model.ChatCompletionResponse {
	return model.ChatCompletionResponse{
		FinishReason:    "tool_calls",
		ProviderName:    languageModel.providerName,
		ModelName:       languageModel.modelName,
		SelectedBackend: "device",
		Message: model.ChatCompletionMessage{
			Role: "assistant",
			ToolCalls: []model.ChatCompletionToolCall{{
				ID:   "scripted-call",
				Type: "function",
				Function: model.ChatCompletionToolCallFunction{
					Name:      toolName,
					Arguments: string(arguments),
				},
			}},
		},
	}
}

func removeActionDiscriminator(document json.RawMessage) json.RawMessage {
	var values map[string]json.RawMessage
	if json.Unmarshal(document, &values) != nil {
		return document
	}
	delete(values, "action")
	normalizedDocument, errorValue := json.Marshal(values)
	if errorValue != nil {
		return document
	}
	return normalizedDocument
}

func structuredRequestFromChat(request model.ChatCompletionRequest) model.StructuredResponseRequest {
	messages := make([]model.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		messages = append(messages, model.Message{Role: message.Role, Content: message.Content})
	}
	return model.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name: request.SchemaName,
		},
		GenerationOptions: request.GenerationOptions,
	}
}

func NewActionScriptedLanguageModel(actionResponses ...string) *ScriptedLanguageModel {
	return NewScriptedLanguageModel(ScriptedLanguageModelOptions{ActionResponses: actionResponses})
}

func (languageModel *ScriptedLanguageModel) EnqueueActionResponses(actionResponses ...string) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.actionResponses = append(languageModel.actionResponses, actionResponses...)
}

func (languageModel *ScriptedLanguageModel) SetActionResponses(actionResponses ...string) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.actionResponses = append([]string{}, actionResponses...)
}

func (languageModel *ScriptedLanguageModel) EnqueueStructuredResponses(schemaName string, responses ...string) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	trimmedSchemaName := strings.TrimSpace(schemaName)
	if trimmedSchemaName == "" {
		return
	}
	languageModel.structuredResponsesBySchema[trimmedSchemaName] = append(languageModel.structuredResponsesBySchema[trimmedSchemaName], responses...)
}

func (languageModel *ScriptedLanguageModel) PendingResponseCounts() map[string]int {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	pendingCounts := map[string]int{}
	if len(languageModel.actionResponses) > 0 {
		pendingCounts["bluecollar_agent_turn_action"] = len(languageModel.actionResponses)
	}
	for schemaName, responses := range languageModel.structuredResponsesBySchema {
		if len(responses) > 0 {
			pendingCounts[schemaName] += len(responses)
		}
	}
	for schemaName, responses := range languageModel.chatResponsesBySchema {
		if len(responses) > 0 {
			pendingCounts[schemaName] += len(responses)
		}
	}
	return pendingCounts
}

func (languageModel *ScriptedLanguageModel) RequestCount() int {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return len(languageModel.requests)
}

func (languageModel *ScriptedLanguageModel) Requests() []model.StructuredResponseRequest {
	return languageModel.RequestsSince(0)
}

func (languageModel *ScriptedLanguageModel) RequestsSince(startIndex int) []model.StructuredResponseRequest {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	if startIndex < 0 || startIndex > len(languageModel.requests) {
		startIndex = 0
	}
	return append([]model.StructuredResponseRequest{}, languageModel.requests[startIndex:]...)
}

func (languageModel *ScriptedLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *ScriptedLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.requests = append(languageModel.requests, request)
	schemaName := strings.TrimSpace(request.StructuredOutputSchema.Name)
	if response, isFound := languageModel.popStructuredResponse(request); isFound {
		return languageModel.structuredResponse(response), nil
	}
	if schemaName == "bluecollar_agent_turn_action" {
		response, errorValue := languageModel.popActionResponse()
		if errorValue != nil {
			return model.StructuredResponse{}, errorValue
		}
		return languageModel.structuredResponse(response), nil
	}
	response := strings.TrimSpace(languageModel.defaultResponsesBySchema[schemaName])
	if response == "" {
		if schemaName == "bluecollar_contract_skill_arbitration" {
			response, errorValue := defaultContractSkillArbitrationResponse(request)
			if errorValue != nil {
				return model.StructuredResponse{}, errorValue
			}
			return languageModel.structuredResponse(response), nil
		}
		if schemaName == "blueclaw_approval_question" {
			return languageModel.structuredResponse(defaultApprovalQuestionResponse(request)), nil
		}
		return model.StructuredResponse{}, noScriptedResponseError{schemaName: schemaName}
	}
	if schemaName == "bluecollar_skill_search_queries" && response == `{"queries":[]}` {
		return languageModel.structuredResponse(defaultSkillSearchQueriesResponse(request)), nil
	}
	return languageModel.structuredResponse(response), nil
}

func defaultContractSkillArbitrationResponse(request model.StructuredResponseRequest) (string, error) {
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Enum []string `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
	}
	if errorValue := json.Unmarshal([]byte(request.StructuredOutputSchema.Document), &schema); errorValue != nil {
		return "", fmt.Errorf("decode contract skill arbitration schema: %w", errorValue)
	}
	skillNames := schema.Properties["selectedSkillNames"].Items.Enum
	selectedSkillNames := append([]string{}, skillNames...)
	rejectedSkillNames := []string{}
	expectedEvidence := []string{}
	document, errorValue := json.Marshal(map[string]any{
		"selectedSkillNames":    selectedSkillNames,
		"rejectedSkillNames":    rejectedSkillNames,
		"requiredNextToolNames": []string{},
		"expectedEvidence":      expectedEvidence,
		"unmetPreconditions":    []string{},
		"reason":                "scripted test default",
	})
	if errorValue != nil {
		return "", fmt.Errorf("encode contract skill arbitration fixture: %w", errorValue)
	}
	return string(document), nil
}

func defaultSkillSearchQueriesResponse(request model.StructuredResponseRequest) string {
	prompt := ""
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == "user" {
			prompt = strings.TrimSpace(request.Messages[index].Content)
			break
		}
	}
	if prompt == "" {
		return `{"queries":[]}`
	}
	document, errorValue := json.Marshal(map[string]any{"queries": []map[string]string{{"description": prompt}}})
	if errorValue != nil {
		return `{"queries":[]}`
	}
	return string(document)
}

// popStructuredResponse dispenses one scripted turn per message for the router
// schema, because one scripted turn is now read twice: the intake decision asks
// with no messages and takes the next turn off the script, and the words call
// that follows carries the turn's prompt and reads that same turn again.
func (languageModel *ScriptedLanguageModel) popStructuredResponse(request model.StructuredResponseRequest) (string, bool) {
	schemaName := strings.TrimSpace(request.StructuredOutputSchema.Name)
	if schemaName == agentcontract.TurnRouterSchemaName && len(request.Messages) > 0 {
		lastResponse, isReplayable := languageModel.lastStructuredResponseBySchema[schemaName]
		if isReplayable {
			return lastResponse, true
		}
	}
	responses := languageModel.structuredResponsesBySchema[schemaName]
	if len(responses) == 0 {
		return "", false
	}
	response := responses[0]
	languageModel.structuredResponsesBySchema[schemaName] = responses[1:]
	if languageModel.lastStructuredResponseBySchema == nil {
		languageModel.lastStructuredResponseBySchema = map[string]string{}
	}
	languageModel.lastStructuredResponseBySchema[schemaName] = response
	return response, true
}

func (languageModel *ScriptedLanguageModel) popActionResponse() (string, error) {
	for index, response := range languageModel.actionResponses {
		if !isActionResponse(response) {
			continue
		}
		languageModel.actionResponses = append(languageModel.actionResponses[:index], languageModel.actionResponses[index+1:]...)
		return response, nil
	}
	return "", fmt.Errorf("scripted language model action response queue is empty")
}

func (languageModel *ScriptedLanguageModel) structuredResponse(content string) model.StructuredResponse {
	return model.StructuredResponse{ProviderName: languageModel.providerName, ModelName: languageModel.modelName, Content: content}
}

func isActionResponse(content string) bool {
	document := map[string]any{}
	if errorValue := json.Unmarshal([]byte(content), &document); errorValue != nil {
		return false
	}
	return stringMapValue(document, "action") != ""
}

func copyResponseQueues(responseQueues map[string][]string) map[string][]string {
	copiedResponseQueues := map[string][]string{}
	for schemaName, responses := range responseQueues {
		copiedResponseQueues[strings.TrimSpace(schemaName)] = append([]string{}, responses...)
	}
	return copiedResponseQueues
}

func mergeDefaultResponses(defaultResponses map[string]string) map[string]string {
	mergedResponses := map[string]string{
		"bluecollar_skill_search_queries": `{"queries":[]}`,
		"bluecollar_execution_plan":       `{"originalInstruction":"scripted test request","summary":"scripted test request","targets":[],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"scripted test request"}`,
		"bluecollar_confirmation_message": `{"reply":"Understood. I will proceed once it is approved."}`,
	}
	for schemaName, response := range defaultResponses {
		mergedResponses[strings.TrimSpace(schemaName)] = response
	}
	return mergedResponses
}

type approvalQuestionContextDocument struct {
	OriginalRequest string            `json:"originalRequest"`
	ModelDraft      string            `json:"modelDraft"`
	ActionDetails   map[string]string `json:"actionDetails"`
}

func defaultApprovalQuestionResponse(request model.StructuredResponseRequest) string {
	contextDocument := approvalQuestionContextFromRequest(request)
	details := contextDocument.ActionDetails
	target := strings.TrimSpace(details["target"])
	content := strings.TrimSpace(firstNonEmpty(details["message"], details["content"], details["title"], details["reason"]))
	question := defaultApprovalQuestionFromContext(contextDocument, target, content)
	document, errorValue := json.Marshal(map[string]string{"question": question})
	if errorValue != nil {
		return `{"question":"should this be approved?"}`
	}
	return string(document)
}

func defaultApprovalQuestionFromContext(contextDocument approvalQuestionContextDocument, target string, content string) string {
	if target != "" && content != "" {
		return target + "should this be sent to?\n\n" + content
	}
	if draftQuestion := approvalQuestionFromDraft(contextDocument.ModelDraft); draftQuestion != "" {
		return draftQuestion
	}
	if requestQuestion := approvalQuestionFromDraft(contextDocument.OriginalRequest); requestQuestion != "" {
		return requestQuestion
	}
	if content != "" {
		return content + "\n\nshould we proceed?"
	}
	return "should this be approved?"
}

func approvalQuestionContextFromRequest(request model.StructuredResponseRequest) approvalQuestionContextDocument {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		var contextDocument approvalQuestionContextDocument
		if json.Unmarshal([]byte(request.Messages[index].Content), &contextDocument) == nil {
			return contextDocument
		}
	}
	return approvalQuestionContextDocument{}
}

func approvalQuestionFromDraft(value string) string {
	trimmedValue := strings.Trim(strings.TrimSpace(value), ".。")
	if trimmedValue == "" {
		return ""
	}
	if strings.HasSuffix(trimmedValue, "?") || strings.HasSuffix(trimmedValue, "？") {
		return trimmedValue
	}
	if strings.HasSuffix(trimmedValue, "doing it") {
		return strings.TrimSuffix(trimmedValue, "doing it") + "shall we?"
	}
	if strings.HasSuffix(trimmedValue, "please do it") {
		return strings.TrimSuffix(trimmedValue, "please do it") + "shall we?"
	}
	return trimmedValue + "?"
}

func stringMapValue(document map[string]any, key string) string {
	value, isString := document[key].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ErrNoScriptedResponse says the script ran out rather than the model failing,
// which a caller that scripts only the calls it expects needs to tell apart.
var ErrNoScriptedResponse = errors.New("scripted language model has no response")

type noScriptedResponseError struct {
	schemaName string
}

func (errorValue noScriptedResponseError) Error() string {
	return "scripted language model has no " + errorValue.schemaName + " response"
}

func (errorValue noScriptedResponseError) Is(target error) bool {
	return target == ErrNoScriptedResponse
}
