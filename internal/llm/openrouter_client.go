package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const DefaultOpenRouterChatCompletionsURL = "https://openrouter.ai/api/v1/chat/completions"
const openRouterErrorBodyMaximumCharacters = 600
const openRouterStructuredResponseRetryInstruction = "respond with ONLY a single valid JSON object matching the schema, no prose, no markdown"
const openRouterStructuredTruncationRetryInstruction = "Your previous response was truncated. Start over and respond with one compact valid JSON object matching the schema. Do not repeat fields, array items, prose, or markdown."
const openRouterStructuredSchemaInstructionPrefix = "Respond with ONLY a single JSON object that validates against this JSON Schema (no prose, no markdown): "

type OpenRouterClient struct {
	APIKey            string
	BaseURL           string
	ModelName         string
	HTTPClient        *http.Client
	AttemptCount      int
	InitialBackoff    time.Duration
	GenerationOptions GenerationOptions
}

type openRouterRequest struct {
	Model          string                `json:"model"`
	Messages       []openRouterMessage   `json:"messages"`
	Stream         bool                  `json:"stream"`
	ResponseFormat *openRouterJSONSchema `json:"response_format,omitempty"`
	Seed           *int64                `json:"seed,omitempty"`
	Temperature    *float64              `json:"temperature,omitempty"`
	MaxTokens      *int                  `json:"max_tokens,omitempty"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openRouterJSONSchema struct {
	Type       string                      `json:"type"`
	JSONSchema openRouterJSONSchemaPayload `json:"json_schema"`
}

type openRouterJSONSchemaPayload struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type openRouterUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type openRouterResponse struct {
	HTTPStatusCode int `json:"-"`
	Choices        []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage openRouterUsage `json:"usage"`
}

type StructuredOutputTruncatedError struct {
	FinishReason string
	ContentBytes int
}

func (errorValue StructuredOutputTruncatedError) Error() string {
	return "structured output truncated before valid json (finish_reason=" + errorValue.FinishReason + ", " + strconv.Itoa(errorValue.ContentBytes) + " bytes); model output exceeded its budget after a compact retry"
}

func openRouterResponseWasTruncated(response openRouterResponse) bool {
	return len(response.Choices) > 0 && response.Choices[0].FinishReason == "length"
}

type OpenRouterRateLimitError struct {
	Message string
}

func (errorValue OpenRouterRateLimitError) Error() string {
	return errorValue.Message
}

func (client OpenRouterClient) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	generationOptions := client.GenerationOptions
	response, errorValue := client.send(responseContext, openRouterRequest{
		Model:       client.modelName(),
		Messages:    []openRouterMessage{{Role: "user", Content: prompt}},
		Stream:      false,
		Seed:        generationOptions.Seed,
		Temperature: generationOptions.Temperature,
	})
	if errorValue != nil {
		return "", errorValue
	}
	return openRouterResponseContent(response), nil
}

func (client OpenRouterClient) GenerateStructuredResponse(responseContext context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	schemaDocument, errorValue := normalizeStructuredOutputSchema(request.StructuredOutputSchema)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	modelName := client.modelName()
	messages := appendOpenRouterStructuredSchemaInstruction(openRouterMessages(modelName, request.Messages), modelName, schemaDocument)
	generationOptions := mergeGenerationOptions(client.GenerationOptions, request.GenerationOptions)
	openRouterStructuredRequest := openRouterRequest{
		Model:       modelName,
		Messages:    messages,
		Stream:      false,
		Seed:        generationOptions.Seed,
		Temperature: generationOptions.Temperature,
		MaxTokens:   generationOptions.MaxTokens,
		ResponseFormat: &openRouterJSONSchema{
			Type: "json_schema",
			JSONSchema: openRouterJSONSchemaPayload{
				Name:   request.StructuredOutputSchema.Name,
				Strict: request.StructuredOutputSchema.IsStrictlyEnforced,
				Schema: schemaDocument,
			},
		},
	}
	response, errorValue := client.send(responseContext, openRouterStructuredRequest)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	content := openRouterStructuredResponseContent(response)
	firstContentError := validateOpenRouterStructuredResponseContent(content)
	if firstContentError == nil {
		return openRouterStructuredResponse(modelName, response, content), nil
	}
	if openRouterResponseWasTruncated(response) {
		return client.retryTruncatedStructuredResponse(responseContext, openRouterStructuredRequest, modelName)
	}
	retryRequest := openRouterStructuredRetryRequest(openRouterStructuredRequest, openRouterResponseContent(response), modelName, schemaDocument)
	retryResponse, errorValue := client.send(responseContext, retryRequest)
	if errorValue != nil {
		return StructuredResponse{}, errors.New("openrouter structured response was not valid json before retry: " + openRouterStructuredResponseErrorSummary(response, content, firstContentError) + "; retry request failed: " + errorValue.Error())
	}
	retryContent := openRouterStructuredResponseContent(retryResponse)
	retryContentError := validateOpenRouterStructuredResponseContent(retryContent)
	if retryContentError != nil {
		if openRouterResponseWasTruncated(retryResponse) {
			return StructuredResponse{}, StructuredOutputTruncatedError{FinishReason: retryResponse.Choices[0].FinishReason, ContentBytes: len(retryContent)}
		}
		return StructuredResponse{}, errors.New("openrouter structured response was not valid json after retry: first " + openRouterStructuredResponseErrorSummary(response, content, firstContentError) + "; retry " + openRouterStructuredResponseErrorSummary(retryResponse, retryContent, retryContentError))
	}
	return openRouterStructuredResponse(modelName, retryResponse, retryContent), nil
}

func (client OpenRouterClient) retryTruncatedStructuredResponse(responseContext context.Context, request openRouterRequest, modelName string) (StructuredResponse, error) {
	retryRequest := request
	retryRequest.Messages = appendMergedOpenRouterMessage(append([]openRouterMessage{}, request.Messages...), openRouterMessage{
		Role:    openRouterMessageRole(modelName, "user"),
		Content: openRouterStructuredTruncationRetryInstruction,
	})
	retryResponse, errorValue := client.send(responseContext, retryRequest)
	if errorValue != nil {
		return StructuredResponse{}, errors.New("openrouter truncated structured response retry failed: " + errorValue.Error())
	}
	retryContent := openRouterStructuredResponseContent(retryResponse)
	if retryContentError := validateOpenRouterStructuredResponseContent(retryContent); retryContentError != nil {
		if openRouterResponseWasTruncated(retryResponse) {
			return StructuredResponse{}, StructuredOutputTruncatedError{FinishReason: retryResponse.Choices[0].FinishReason, ContentBytes: len(retryContent)}
		}
		return StructuredResponse{}, errors.New("openrouter truncated structured response retry was not valid json: " + openRouterStructuredResponseErrorSummary(retryResponse, retryContent, retryContentError))
	}
	return openRouterStructuredResponse(modelName, retryResponse, retryContent), nil
}

func mergeGenerationOptions(defaultOptions GenerationOptions, requestOptions GenerationOptions) GenerationOptions {
	result := defaultOptions
	if requestOptions.Seed != nil {
		result.Seed = requestOptions.Seed
	}
	if requestOptions.Temperature != nil {
		result.Temperature = requestOptions.Temperature
	}
	if requestOptions.MaxTokens != nil {
		result.MaxTokens = requestOptions.MaxTokens
	}
	return result
}

func (client OpenRouterClient) send(ctx context.Context, request openRouterRequest) (openRouterResponse, error) {
	if strings.TrimSpace(client.APIKey) == "" {
		return openRouterResponse{}, errors.New("openrouter api key is not configured")
	}
	var response openRouterResponse
	errorValue := retryOpenRouterRateLimit(ctx, client.attemptCount(), client.initialBackoff(), func() error {
		nextResponse, nextError := client.sendOnce(ctx, request)
		if nextError != nil {
			return nextError
		}
		response = nextResponse
		return nil
	})
	return response, errorValue
}

func (client OpenRouterClient) sendOnce(ctx context.Context, request openRouterRequest) (openRouterResponse, error) {
	requestDocument, errorValue := json.Marshal(request)
	if errorValue != nil {
		return openRouterResponse{}, errorValue
	}
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL(), bytes.NewReader(requestDocument))
	if errorValue != nil {
		return openRouterResponse{}, errorValue
	}
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(client.APIKey))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, errorValue := client.httpClient().Do(httpRequest)
	if errorValue != nil {
		return openRouterResponse{}, errorValue
	}
	defer httpResponse.Body.Close()
	responseDocument, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return openRouterResponse{}, errors.New("read openrouter response: " + errorValue.Error())
	}
	if httpResponse.StatusCode == http.StatusTooManyRequests {
		return openRouterResponse{}, OpenRouterRateLimitError{Message: openRouterHTTPErrorMessage(httpResponse.StatusCode, responseDocument)}
	}
	if httpResponse.StatusCode >= http.StatusBadRequest {
		return openRouterResponse{}, errors.New(openRouterHTTPErrorMessage(httpResponse.StatusCode, responseDocument))
	}
	var response openRouterResponse
	if errorValue := json.Unmarshal(responseDocument, &response); errorValue != nil {
		return openRouterResponse{}, errorValue
	}
	response.HTTPStatusCode = httpResponse.StatusCode
	if len(response.Choices) == 0 {
		return openRouterResponse{}, errors.New("openrouter response did not include choices: " + openRouterHTTPErrorMessage(httpResponse.StatusCode, responseDocument))
	}
	if strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return openRouterResponse{}, errors.New("openrouter returned an answer with no content, finish reason " + strconv.Quote(response.Choices[0].FinishReason) + "; the model was asked for " + strconv.Quote(request.ResponseFormatName()) + " and produced none")
	}
	return response, nil
}

func retryOpenRouterRateLimit(ctx context.Context, attemptCount int, initialBackoff time.Duration, action func() error) error {
	backoff := initialBackoff
	for attempt := 1; attempt <= attemptCount; attempt++ {
		errorValue := action()
		if errorValue == nil {
			return nil
		}
		if !isOpenRouterRateLimit(errorValue) || attempt == attemptCount {
			return errorValue
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
	}
	return nil
}

func isOpenRouterRateLimit(errorValue error) bool {
	var rateLimitError OpenRouterRateLimitError
	return errors.As(errorValue, &rateLimitError)
}

func (client OpenRouterClient) httpClient() *http.Client {
	if client.HTTPClient != nil {
		return client.HTTPClient
	}
	return &http.Client{}
}

func (client OpenRouterClient) baseURL() string {
	trimmedBaseURL := strings.TrimSpace(client.BaseURL)
	if trimmedBaseURL != "" {
		return trimmedBaseURL
	}
	return DefaultOpenRouterChatCompletionsURL
}

func (client OpenRouterClient) modelName() string {
	return strings.TrimSpace(client.ModelName)
}

func (client OpenRouterClient) attemptCount() int {
	if client.AttemptCount > 0 {
		return client.AttemptCount
	}
	return 3
}

func (client OpenRouterClient) initialBackoff() time.Duration {
	if client.InitialBackoff > 0 {
		return client.InitialBackoff
	}
	return 750 * time.Millisecond
}

func openRouterMessages(modelName string, messages []Message) []openRouterMessage {
	result := make([]openRouterMessage, 0, len(messages))
	for _, message := range messages {
		mappedMessage := openRouterMessage{
			Role:    openRouterMessageRole(modelName, message.Role),
			Content: openRouterMessageContent(message),
		}
		if mappedMessage.Role != message.Role {
			mappedMessage.Content = openRouterInstructionContent(message.Role, mappedMessage.Content)
		}
		result = appendMergedOpenRouterMessage(result, mappedMessage)
	}
	return result
}

func appendOpenRouterStructuredSchemaInstruction(messages []openRouterMessage, modelName string, schemaDocument json.RawMessage) []openRouterMessage {
	result := make([]openRouterMessage, 0, len(messages)+1)
	lastUserMessageIndex := -1
	for messageIndex, message := range messages {
		if strings.TrimSpace(message.Role) == "user" {
			lastUserMessageIndex = messageIndex
		}
	}
	for messageIndex, message := range messages {
		result = appendMergedOpenRouterMessage(result, message)
		if messageIndex == lastUserMessageIndex {
			result = appendMergedOpenRouterMessage(result, openRouterStructuredSchemaInstructionMessage(modelName, schemaDocument))
		}
	}
	if lastUserMessageIndex == -1 {
		result = appendMergedOpenRouterMessage(result, openRouterStructuredSchemaInstructionMessage(modelName, schemaDocument))
	}
	return result
}

func openRouterStructuredSchemaInstructionMessage(modelName string, schemaDocument json.RawMessage) openRouterMessage {
	role := openRouterMessageRole(modelName, "system")
	message := openRouterMessage{
		Role:    role,
		Content: openRouterStructuredSchemaInstruction(schemaDocument),
	}
	if role != "system" {
		message.Content = openRouterInstructionContent("system", message.Content)
	}
	return message
}

func openRouterStructuredSchemaInstruction(schemaDocument json.RawMessage) string {
	return openRouterStructuredSchemaInstructionPrefix + string(schemaDocument)
}

func openRouterMessageRole(modelName string, role string) string {
	normalizedModelName := strings.ToLower(strings.TrimSpace(modelName))
	normalizedRole := strings.ToLower(strings.TrimSpace(role))
	if strings.HasPrefix(normalizedModelName, "google/gemma-") && (normalizedRole == "system" || normalizedRole == "developer") {
		return "user"
	}
	return strings.TrimSpace(role)
}

func openRouterInstructionContent(role string, content any) any {
	text, isText := content.(string)
	if !isText {
		return content
	}
	trimmedRole := strings.TrimSpace(role)
	if trimmedRole == "" {
		return text
	}
	return trimmedRole + " instruction:\n" + text
}

func appendMergedOpenRouterMessage(messages []openRouterMessage, message openRouterMessage) []openRouterMessage {
	if len(messages) == 0 {
		return append(messages, message)
	}
	previousMessage := messages[len(messages)-1]
	if previousMessage.Role != message.Role {
		return append(messages, message)
	}
	previousText, isPreviousText := previousMessage.Content.(string)
	nextText, isNextText := message.Content.(string)
	if !isPreviousText || !isNextText {
		return append(messages, message)
	}
	messages[len(messages)-1].Content = strings.TrimSpace(previousText) + "\n\n" + strings.TrimSpace(nextText)
	return messages
}

func openRouterMessageContent(message Message) any {
	if len(message.Parts) == 0 {
		return message.Content
	}
	parts := []map[string]any{}
	if strings.TrimSpace(message.Content) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": message.Content})
	}
	for _, part := range message.Parts {
		if strings.TrimSpace(part.Type) == "text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, map[string]any{"type": "text", "text": part.Text})
		}
		if strings.TrimSpace(part.Type) == "image" && strings.TrimSpace(part.DataBase64) != "" && strings.TrimSpace(part.MimeType) != "" {
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]string{
					"url": "data:" + strings.TrimSpace(part.MimeType) + ";base64," + strings.TrimSpace(part.DataBase64),
				},
			})
		}
	}
	if len(parts) == 0 {
		return message.Content
	}
	return parts
}

func openRouterResponseContent(response openRouterResponse) string {
	if len(response.Choices) == 0 {
		return ""
	}
	return response.Choices[0].Message.Content
}

func openRouterStructuredResponseContent(response openRouterResponse) string {
	return stripMarkdownJSONFence(openRouterResponseContent(response))
}

func openRouterStructuredResponse(modelName string, response openRouterResponse, content string) StructuredResponse {
	return StructuredResponse{
		ProviderName: "openrouter",
		ModelName:    modelName,
		Content:      content,
		Usage: Usage{
			PromptTokens:     response.Usage.PromptTokens,
			CompletionTokens: response.Usage.CompletionTokens,
			TotalTokens:      openRouterTotalTokens(response.Usage),
		},
	}
}

func openRouterStructuredRetryRequest(request openRouterRequest, returnedContent string, modelName string, schemaDocument json.RawMessage) openRouterRequest {
	retryRequest := request
	retryRequest.Messages = append([]openRouterMessage{}, request.Messages...)
	retryRequest.Messages = append(retryRequest.Messages, openRouterMessage{Role: "assistant", Content: returnedContent})
	retryRequest.Messages = append(retryRequest.Messages, openRouterMessage{Role: "user", Content: openRouterStructuredResponseRetryInstruction})
	retryRequest.Messages = appendOpenRouterStructuredSchemaInstruction(retryRequest.Messages, modelName, schemaDocument)
	return retryRequest
}

func validateOpenRouterStructuredResponseContent(content string) error {
	var responseDocument json.RawMessage
	return json.Unmarshal([]byte(content), &responseDocument)
}

func openRouterStructuredResponseErrorSummary(response openRouterResponse, content string, errorValue error) string {
	return "status=" + openRouterStatusText(response.HTTPStatusCode) + " code=" + strconv.Itoa(response.HTTPStatusCode) + " parseError=" + errorValue.Error() + " content=" + truncateOpenRouterErrorBody(content)
}

func stripMarkdownJSONFence(value string) string {
	trimmedValue := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmedValue, "```") {
		return trimmedValue
	}
	lines := strings.Split(trimmedValue, "\n")
	if len(lines) < 2 {
		return trimmedValue
	}
	lines = lines[1:]
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func openRouterTotalTokens(usage openRouterUsage) int64 {
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.PromptTokens + usage.CompletionTokens
}

func openRouterHTTPErrorMessage(statusCode int, responseDocument []byte) string {
	return "openrouter status=" + http.StatusText(statusCode) + " code=" + strconv.Itoa(statusCode) + " body=" + truncateOpenRouterErrorBody(string(responseDocument))
}

func openRouterStatusText(statusCode int) string {
	statusText := http.StatusText(statusCode)
	if statusText != "" {
		return statusText
	}
	return "unknown"
}

func truncateOpenRouterErrorBody(value string) string {
	trimmedValue := strings.Join(strings.Fields(value), " ")
	if len([]rune(trimmedValue)) <= openRouterErrorBodyMaximumCharacters {
		return trimmedValue
	}
	return string([]rune(trimmedValue)[:openRouterErrorBodyMaximumCharacters]) + "..."
}

func (request openRouterRequest) ResponseFormatName() string {
	if request.ResponseFormat == nil {
		return ""
	}
	return request.ResponseFormat.JSONSchema.Name
}
