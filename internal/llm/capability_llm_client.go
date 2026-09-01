package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

type CapabilityLLMClient struct {
	CapabilityClient capability.Client
	ModelName        string
	ExecutionMode    string
}

type capabilityStructuredResponseRequestDocument struct {
	Model                  string                           `json:"model,omitempty"`
	ExecutionMode          string                           `json:"executionMode"`
	Context                *RequestContext                  `json:"context,omitempty"`
	Messages               []Message                        `json:"messages"`
	StructuredOutputSchema capabilityStructuredOutputSchema `json:"structuredOutputSchema"`
	GenerationOptions      *GenerationOptions               `json:"generationOptions,omitempty"`
}

type capabilityTextResponseRequestDocument struct {
	Model         string          `json:"model,omitempty"`
	ExecutionMode string          `json:"executionMode"`
	Context       *RequestContext `json:"context,omitempty"`
	Messages      []Message       `json:"messages"`
}

type capabilityChatCompletionRequestDocument struct {
	Model             string                  `json:"model,omitempty"`
	ExecutionMode     string                  `json:"executionMode"`
	Context           *RequestContext         `json:"context,omitempty"`
	Messages          []ChatCompletionMessage `json:"messages"`
	Tools             []ChatCompletionTool    `json:"tools,omitempty"`
	ToolChoice        json.RawMessage         `json:"toolChoice,omitempty"`
	ParallelToolCalls bool                    `json:"parallelToolCalls"`
	GenerationOptions *GenerationOptions      `json:"generationOptions,omitempty"`
}

type capabilityStructuredOutputSchema struct {
	Name               string          `json:"name"`
	Document           json.RawMessage `json:"document"`
	IsStrictlyEnforced bool            `json:"isStrictlyEnforced"`
}

type capabilityStructuredResponseDocument struct {
	ProviderName    string `json:"provider"`
	ModelName       string `json:"model"`
	Content         string `json:"content"`
	SelectedBackend string `json:"selectedBackend"`
	FinishReason    string `json:"finishReason,omitempty"`
	ConstraintMode  string `json:"constraintMode,omitempty"`
	Usage           Usage  `json:"usage"`
	// capabilityd keeps a chain of its own, so the model that answers is not
	// always the tier that was asked for. The ledger has carried these two
	// fields all along; only this hop dropped them, which is why a turn
	// answered by a device model looked like an ordinary one.
	UsedFallback   bool   `json:"usedFallback,omitempty"`
	FallbackReason string `json:"fallbackReason,omitempty"`
}

func (capabilityLLMClient CapabilityLLMClient) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	return capabilityLLMClient.generateResponse(responseContext, prompt, capabilityLLMClient.executionMode())
}

func (capabilityLLMClient CapabilityLLMClient) GenerateRecoveryResponse(responseContext context.Context, prompt string) (string, error) {
	autoContext, cancelAuto := recoveryAttemptContext(responseContext)
	response, errorValue := capabilityLLMClient.generateResponse(autoContext, prompt, "auto")
	cancelAuto()
	if errorValue == nil && strings.TrimSpace(response) != "" {
		return response, nil
	}
	if capabilityLLMClient.executionMode() == "device" {
		return response, errorValue
	}
	deviceContext, cancelDevice := recoveryAttemptContext(responseContext)
	defer cancelDevice()
	return capabilityLLMClient.generateResponse(deviceContext, prompt, "device")
}

func (capabilityLLMClient CapabilityLLMClient) GenerateLocalRecoveryResponse(responseContext context.Context, prompt string) (string, error) {
	deviceContext, cancelDevice := recoveryAttemptContext(responseContext)
	defer cancelDevice()
	return capabilityLLMClient.generateResponse(deviceContext, prompt, "device")
}

func (capabilityLLMClient CapabilityLLMClient) GenerateRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	if capabilityLLMClient.executionMode() == "device" {
		return capabilityLLMClient.generateRecoveryChatAttempt(responseContext, request, "device")
	}
	response, errorValue := capabilityLLMClient.generateRecoveryChatAttempt(responseContext, request, "auto")
	if errorValue == nil {
		return response, nil
	}
	if contextError := responseContext.Err(); contextError != nil {
		return response, contextError
	}
	return capabilityLLMClient.generateRecoveryChatAttempt(responseContext, request, "device")
}

func (capabilityLLMClient CapabilityLLMClient) generateRecoveryChatAttempt(responseContext context.Context, request ChatCompletionRequest, executionMode string) (ChatCompletionResponse, error) {
	autoContext, cancelAuto := recoveryAttemptContext(responseContext)
	response, errorValue := capabilityLLMClient.generateChatCompletion(autoContext, request, executionMode)
	cancelAuto()
	if errorValue != nil {
		return response, errorValue
	}
	if executionMode == "device" && response.SelectedBackend != "device" {
		return response, errors.New("device recovery chat returned a non-device backend")
	}
	_, errorValue = RecoveryChatCompletionText(response)
	return response, errorValue
}

func (capabilityLLMClient CapabilityLLMClient) GenerateLocalRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	return capabilityLLMClient.generateRecoveryChatAttempt(responseContext, request, "device")
}

func recoveryAttemptContext(responseContext context.Context) (context.Context, context.CancelFunc) {
	baseContext := context.Background()
	requestContext := RequestContextFromContext(responseContext)
	if requestContext != (RequestContext{}) {
		baseContext = ContextWithRequestContext(baseContext, requestContext)
	}
	if deadline, hasDeadline := responseContext.Deadline(); hasDeadline {
		return context.WithDeadline(baseContext, deadline)
	}
	return context.WithCancel(baseContext)
}

const llmTransientRetryCount = 4

func isTransientLLMTransportError(errorValue error) bool {
	if errorValue == nil {
		return false
	}
	message := strings.ToLower(errorValue.Error())
	for _, marker := range []string{"eof", "connection refused", "connection reset", "broken pipe", "no such host", "i/o timeout", "server closed", "connection closed", "no route to host"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (capabilityLLMClient CapabilityLLMClient) postJSONWithRetry(responseContext context.Context, path string, requestDocument any, responseDocument any) error {
	var errorValue error
	for attempt := 0; attempt < llmTransientRetryCount; attempt++ {
		if attempt > 0 {
			select {
			case <-responseContext.Done():
				return responseContext.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		errorValue = capabilityLLMClient.CapabilityClient.PostJSON(responseContext, path, requestDocument, responseDocument)
		if errorValue == nil || !isTransientLLMTransportError(errorValue) {
			return errorValue
		}
	}
	return errorValue
}

func (capabilityLLMClient CapabilityLLMClient) generateResponse(responseContext context.Context, prompt string, executionMode string) (string, error) {
	if capabilityLLMClient.CapabilityClient.HTTPClient == nil {
		return "", errors.New("capability llm http client is not configured")
	}

	var responseDocument capabilityStructuredResponseDocument
	errorValue := capabilityLLMClient.postJSONWithRetry(
		responseContext,
		"/v1/llm/text",
		capabilityTextResponseRequestDocument{
			Model:         capabilityLLMClient.ModelName,
			ExecutionMode: executionMode,
			Context:       requestContextPointer(responseContext),
			Messages: []Message{{
				Role:    "user",
				Content: prompt,
			}},
		},
		&responseDocument,
	)
	if errorValue != nil {
		return "", errorValue
	}

	return responseDocument.Content, nil
}

func (capabilityLLMClient CapabilityLLMClient) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	if capabilityLLMClient.CapabilityClient.HTTPClient == nil {
		return StructuredResponse{}, errors.New("capability llm http client is not configured")
	}

	requestDocument, errorValue := capabilityLLMClient.buildStructuredRequestDocument(responseContext, structuredResponseRequest)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	var responseDocument capabilityStructuredResponseDocument
	errorValue = capabilityLLMClient.postJSONWithRetry(responseContext, "/v1/llm/structured", requestDocument, &responseDocument)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	providerName := strings.TrimSpace(responseDocument.ProviderName)
	if providerName == "" {
		providerName = "capabilityLLM"
	}
	modelName := strings.TrimSpace(responseDocument.ModelName)
	if modelName == "" {
		modelName = capabilityLLMClient.ModelName
	}

	return StructuredResponse{
		Transport:       "capability",
		ProviderName:    providerName,
		ModelName:       modelName,
		Content:         responseDocument.Content,
		SelectedBackend: responseDocument.SelectedBackend,
		FinishReason:    responseDocument.FinishReason,
		ConstraintMode:  responseDocument.ConstraintMode,
		Usage:           responseDocument.Usage,
		UsedFallback:    responseDocument.UsedFallback,
		FallbackReason:  responseDocument.FallbackReason,
	}, nil
}

func (capabilityLLMClient CapabilityLLMClient) GenerateChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	return capabilityLLMClient.generateChatCompletion(responseContext, request, capabilityLLMClient.executionMode())
}

func (capabilityLLMClient CapabilityLLMClient) generateChatCompletion(responseContext context.Context, request ChatCompletionRequest, executionMode string) (ChatCompletionResponse, error) {
	if capabilityLLMClient.CapabilityClient.HTTPClient == nil {
		return ChatCompletionResponse{}, errors.New("capability llm http client is not configured")
	}

	requestDocument := capabilityChatCompletionRequestDocument{
		Model:             requestedModelNameOrDefault(request, capabilityLLMClient.ModelName),
		ExecutionMode:     executionMode,
		Context:           requestContextPointer(responseContext),
		Messages:          append([]ChatCompletionMessage{}, request.Messages...),
		Tools:             append([]ChatCompletionTool{}, request.Tools...),
		ToolChoice:        append(json.RawMessage{}, request.ToolChoice...),
		ParallelToolCalls: request.ParallelToolCalls,
		GenerationOptions: generationOptionsPointer(request.GenerationOptions),
	}

	var response ChatCompletionResponse
	errorValue := capabilityLLMClient.postJSONWithRetry(responseContext, "/v1/llm/chat", requestDocument, &response)
	if errorValue != nil {
		return ChatCompletionResponse{}, errorValue
	}
	if strings.TrimSpace(response.ProviderName) == "" {
		response.ProviderName = "capabilityLLM"
	}
	if strings.TrimSpace(response.ModelName) == "" {
		response.ModelName = capabilityLLMClient.ModelName
	}
	if response.Message.ToolCalls == nil {
		response.Message.ToolCalls = []ChatCompletionToolCall{}
	}
	response.Transport = "capability"
	return response, nil
}

func (capabilityLLMClient CapabilityLLMClient) buildStructuredRequestDocument(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (capabilityStructuredResponseRequestDocument, error) {
	jsonSchemaDocument, errorValue := normalizeStructuredOutputSchema(structuredResponseRequest.StructuredOutputSchema)
	if errorValue != nil {
		return capabilityStructuredResponseRequestDocument{}, errorValue
	}

	return capabilityStructuredResponseRequestDocument{
		Model:             capabilityLLMClient.ModelName,
		ExecutionMode:     capabilityLLMClient.executionMode(),
		Context:           requestContextPointer(responseContext),
		Messages:          append([]Message{}, structuredResponseRequest.Messages...),
		GenerationOptions: generationOptionsPointer(structuredResponseRequest.GenerationOptions),
		StructuredOutputSchema: capabilityStructuredOutputSchema{
			Name:               structuredResponseRequest.StructuredOutputSchema.Name,
			Document:           jsonSchemaDocument,
			IsStrictlyEnforced: structuredResponseRequest.StructuredOutputSchema.IsStrictlyEnforced,
		},
	}, nil
}

func requestedModelNameOrDefault(request ChatCompletionRequest, defaultModelName string) string {
	if requestedModelName := strings.TrimSpace(request.ModelName); requestedModelName != "" {
		return requestedModelName
	}
	return defaultModelName
}

func generationOptionsPointer(options GenerationOptions) *GenerationOptions {
	if options.Seed == nil && options.Temperature == nil && options.MaxTokens == nil {
		return nil
	}
	return &options
}

func requestContextPointer(ctx context.Context) *RequestContext {
	requestContext := RequestContextFromContext(ctx)
	if requestContext.RequesterPersonID == "" &&
		requestContext.RequesterEmail == "" &&
		requestContext.RequesterName == "" &&
		requestContext.RequesterPlatformUserID == "" &&
		requestContext.ConversationID == "" &&
		requestContext.Platform == "" {
		return nil
	}
	return &requestContext
}

func (capabilityLLMClient CapabilityLLMClient) executionMode() string {
	executionMode := strings.TrimSpace(capabilityLLMClient.ExecutionMode)
	if executionMode == "" {
		return "auto"
	}
	return executionMode
}
