package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

func TestCapabilityLLMClientSendsStructuredRequestWithoutAuthorization(t *testing.T) {
	var receivedAuthorization string
	var receivedDocument capabilityStructuredResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/llm/structured" {
			t.Fatalf("expected structured llm path, got %q", request.URL.Path)
		}
		receivedAuthorization = request.Header.Get("Authorization")
		errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument)
		if errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma-4-E4B-it","content":"{\"reply\":\"hello\"}","selectedBackend":"gpu","constraintMode":"openai_json_schema"}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma-4-E4B-it",
		ExecutionMode: "local",
	}

	structuredResponse, errorValue := client.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue != nil {
		t.Fatalf("expected structured response: %v", errorValue)
	}

	if receivedAuthorization != "" {
		t.Fatalf("expected no authorization header, got %q", receivedAuthorization)
	}
	if receivedDocument.Model != "gemma-4-E4B-it" {
		t.Fatalf("expected model to be passed through, got %q", receivedDocument.Model)
	}
	if receivedDocument.ExecutionMode != "local" {
		t.Fatalf("expected execution mode to be passed through, got %q", receivedDocument.ExecutionMode)
	}
	if string(receivedDocument.StructuredOutputSchema.Document) != `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"],"additionalProperties":false}` {
		t.Fatalf("expected schema document to be unchanged, got %s", string(receivedDocument.StructuredOutputSchema.Document))
	}
	if receivedDocument.GenerationOptions != nil {
		t.Fatalf("expected empty generation options to be omitted, got %+v", receivedDocument.GenerationOptions)
	}
	if structuredResponse.Content != `{"reply":"hello"}` {
		t.Fatalf("expected capability content to be returned, got %q", structuredResponse.Content)
	}
	if structuredResponse.ModelName != "gemma-4-E4B-it" {
		t.Fatalf("expected model name from capability response, got %q", structuredResponse.ModelName)
	}
	if structuredResponse.ConstraintMode != "openai_json_schema" {
		t.Fatalf("expected constraint mode from capability response, got %q", structuredResponse.ConstraintMode)
	}
}

func TestCapabilityLLMClientRoundTripsUsage(t *testing.T) {
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma-4-E4B-it","content":"{\"reply\":\"ok\"}","selectedBackend":"gpu","constraintMode":"openai_json_schema","usage":{"promptTokens":123,"completionTokens":45,"totalTokens":168}}`), nil
	}}
	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName: "gemma-4-E4B-it",
	}

	structuredResponse, errorValue := client.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue != nil {
		t.Fatalf("expected structured response: %v", errorValue)
	}
	if structuredResponse.Usage.PromptTokens != 123 {
		t.Fatalf("expected prompt tokens 123, got %d", structuredResponse.Usage.PromptTokens)
	}
	if structuredResponse.Usage.CompletionTokens != 45 {
		t.Fatalf("expected completion tokens 45, got %d", structuredResponse.Usage.CompletionTokens)
	}
	if structuredResponse.Usage.TotalTokens != 168 {
		t.Fatalf("expected total tokens 168, got %d", structuredResponse.Usage.TotalTokens)
	}
}

func TestCapabilityLLMClientReturnsCapabilityError(t *testing.T) {
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		return jsonCapabilityResponse(http.StatusBadGateway, "local model unavailable"), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName: "gemma-4-E4B-it",
	}

	_, errorValue := client.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue == nil {
		t.Fatal("expected capability error")
	}
	if errorValue.Error() != "local model unavailable" {
		t.Fatalf("expected capability error body, got %q", errorValue.Error())
	}
}

func TestCapabilityLLMClientForwardsGenerationOptions(t *testing.T) {
	seed := int64(1234)
	temperature := 0.2
	var receivedDocument capabilityStructuredResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"content":"{\"reply\":\"ok\"}"}`), nil
	}}
	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName: "gemma",
	}
	request := buildTestStructuredResponseRequest()
	request.GenerationOptions = GenerationOptions{Seed: &seed, Temperature: &temperature}

	_, errorValue := client.GenerateStructuredResponse(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected structured response: %v", errorValue)
	}
	if receivedDocument.GenerationOptions == nil || receivedDocument.GenerationOptions.Seed == nil || *receivedDocument.GenerationOptions.Seed != seed {
		t.Fatalf("expected seed to be forwarded, got %+v", receivedDocument.GenerationOptions)
	}
	if receivedDocument.GenerationOptions.Temperature == nil || *receivedDocument.GenerationOptions.Temperature != temperature {
		t.Fatalf("expected temperature to be forwarded, got %+v", receivedDocument.GenerationOptions)
	}
}

func TestCapabilityLLMClientGenerateResponseUsesTextEndpoint(t *testing.T) {
	var receivedDocument capabilityTextResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/llm/text" {
			t.Fatalf("expected text llm path, got %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("expected no authorization header, got %q", request.Header.Get("Authorization"))
		}
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"plain reply","selectedBackend":"cpu"}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma",
		ExecutionMode: "local",
	}

	response, errorValue := client.GenerateResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected text response: %v", errorValue)
	}
	if response != "plain reply" {
		t.Fatalf("expected plain text response, got %q", response)
	}
	if receivedDocument.Model != "gemma" || receivedDocument.ExecutionMode != "local" {
		t.Fatalf("expected model and execution mode, got %+v", receivedDocument)
	}
	if len(receivedDocument.Messages) != 1 || receivedDocument.Messages[0].Content != "hello" {
		t.Fatalf("expected prompt message, got %+v", receivedDocument.Messages)
	}
}

func TestCapabilityLLMClientGenerateChatCompletionSendsNativeToolRequest(t *testing.T) {
	seed := int64(42)
	temperature := 0.2
	maxTokens := 192
	var receivedDocument map[string]any
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/llm/chat" {
			t.Fatalf("expected chat llm path, got %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("expected no authorization header, got %q", request.Header.Get("Authorization"))
		}
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"finishReason":"tool_calls","provider":"capabilityLLM","model":"gemma","message":{"role":"assistant","content":"","toolCalls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"status\"}"}}]},"usage":{"promptTokens":4,"completionTokens":2,"totalTokens":6}}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma",
		ExecutionMode: "remote",
	}

	response, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{
		Messages: []ChatCompletionMessage{{Role: "user", Content: "check"}},
		Tools: []ChatCompletionTool{{
			Type: "function",
			Function: ChatCompletionFunction{
				Name:        "lookup",
				Description: "Lookup status",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
			},
		}},
		ToolChoice:        json.RawMessage(`{"type":"function","function":{"name":"lookup"}}`),
		ParallelToolCalls: true,
		GenerationOptions: GenerationOptions{Seed: &seed, Temperature: &temperature, MaxTokens: &maxTokens},
	})

	if errorValue != nil {
		t.Fatalf("expected chat completion response: %v", errorValue)
	}
	if receivedDocument["model"] != "gemma" || receivedDocument["executionMode"] != "remote" {
		t.Fatalf("expected model and execution mode, got %+v", receivedDocument)
	}
	tools := receivedDocument["tools"].([]any)
	tool := tools[0].(map[string]any)
	function := tool["function"].(map[string]any)
	if tool["type"] != "function" || function["name"] != "lookup" {
		t.Fatalf("expected function tool, got %+v", tool)
	}
	toolChoice := receivedDocument["toolChoice"].(map[string]any)
	toolChoiceFunction := toolChoice["function"].(map[string]any)
	if toolChoice["type"] != "function" || toolChoiceFunction["name"] != "lookup" {
		t.Fatalf("expected object tool choice, got %+v", toolChoice)
	}
	if receivedDocument["parallelToolCalls"] != true {
		t.Fatalf("expected parallel tool calls to be forwarded, got %+v", receivedDocument)
	}
	generationOptions := receivedDocument["generationOptions"].(map[string]any)
	if generationOptions["seed"] != float64(seed) || generationOptions["temperature"] != temperature || generationOptions["maxTokens"] != float64(maxTokens) {
		t.Fatalf("expected generation options to be forwarded, got %+v", generationOptions)
	}
	if response.FinishReason != "tool_calls" {
		t.Fatalf("expected tool_calls finish reason, got %q", response.FinishReason)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v", response.Message.ToolCalls)
	}
	toolCall := response.Message.ToolCalls[0]
	if toolCall.Function.Name != "lookup" || toolCall.Function.Arguments != `{"query":"status"}` {
		t.Fatalf("expected JSON string arguments, got %+v", toolCall)
	}
	if response.Usage.TotalTokens != 6 {
		t.Fatalf("expected usage to round trip, got %+v", response.Usage)
	}
}

func TestCapabilityLLMClientGenerateChatCompletionForwardsStringToolChoice(t *testing.T) {
	var receivedDocument map[string]any
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"finishReason":"stop","message":{"role":"assistant","content":"done","toolCalls":[]}}`), nil
	}}
	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName: "gemma",
	}

	response, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{
		Messages:          []ChatCompletionMessage{{Role: "user", Content: "hello"}},
		ToolChoice:        json.RawMessage(`"auto"`),
		ParallelToolCalls: false,
	})

	if errorValue != nil {
		t.Fatalf("expected chat completion response: %v", errorValue)
	}
	if receivedDocument["toolChoice"] != "auto" {
		t.Fatalf("expected string tool choice, got %+v", receivedDocument)
	}
	if receivedDocument["parallelToolCalls"] != false {
		t.Fatalf("expected parallel tool calls false, got %+v", receivedDocument)
	}
	if response.ProviderName != "capabilityLLM" || response.ModelName != "gemma" {
		t.Fatalf("expected default provider and model, got %+v", response)
	}
	if response.Message.Content != "done" {
		t.Fatalf("expected assistant content, got %q", response.Message.Content)
	}
}

func TestCapabilityLLMClientSendsRequesterContext(t *testing.T) {
	var receivedDocument capabilityStructuredResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"{\"reply\":\"ok\"}","selectedBackend":"companion_local"}`), nil
	}}
	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName: "gemma",
	}
	requestContext := RequestContext{
		RequesterPersonID:       "person-1",
		RequesterEmail:          "alice@example.com",
		RequesterName:           "Alice",
		RequesterPlatformUserID: "user-1",
		ConversationID:          "dm:channel-1",
		Platform:                "mattermost",
	}
	_, errorValue := client.GenerateStructuredResponse(ContextWithRequestContext(context.Background(), requestContext), buildTestStructuredResponseRequest())
	if errorValue != nil {
		t.Fatalf("expected structured response: %v", errorValue)
	}
	if receivedDocument.Context == nil || *receivedDocument.Context != requestContext {
		t.Fatalf("expected requester context, got %+v", receivedDocument.Context)
	}
}

func TestCapabilityLLMClientRecoveryResponseUsesLocalCapableExecutionMode(t *testing.T) {
	receivedExecutionModes := []string{}
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		var receivedDocument capabilityTextResponseRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		receivedExecutionModes = append(receivedExecutionModes, receivedDocument.ExecutionMode)
		if receivedDocument.ExecutionMode == "auto" {
			return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"local-ish reply","selectedBackend":"cpu"}`), nil
		}
		return jsonCapabilityResponse(http.StatusBadGateway, "remote unavailable"), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma",
		ExecutionMode: "remote",
	}

	response, errorValue := client.GenerateRecoveryResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected recovery text response: %v", errorValue)
	}
	if response != "local-ish reply" {
		t.Fatalf("expected recovery response, got %q", response)
	}
	if len(receivedExecutionModes) != 1 || receivedExecutionModes[0] != "auto" {
		t.Fatalf("expected recovery to use auto execution mode, got %+v", receivedExecutionModes)
	}
}

func TestCapabilityLLMClientRecoveryResponseFallsBackToDeviceAfterAutoFailure(t *testing.T) {
	receivedExecutionModes := []string{}
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		var receivedDocument capabilityTextResponseRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		receivedExecutionModes = append(receivedExecutionModes, receivedDocument.ExecutionMode)
		if receivedDocument.ExecutionMode == "auto" {
			return jsonCapabilityResponse(http.StatusBadGateway, "remote unavailable"), nil
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"device recovery reply","selectedBackend":"device"}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma",
		ExecutionMode: "remote",
	}

	response, errorValue := client.GenerateRecoveryResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected recovery text response: %v", errorValue)
	}
	if response != "device recovery reply" {
		t.Fatalf("expected device recovery response, got %q", response)
	}
	if strings.Join(receivedExecutionModes, ",") != "auto,device" {
		t.Fatalf("expected auto then device execution modes, got %+v", receivedExecutionModes)
	}
}

func TestCapabilityLLMClientLocalRecoveryResponseUsesDeviceExecutionMode(t *testing.T) {
	var receivedDocument capabilityTextResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"local failure notice","selectedBackend":"device"}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma",
		ExecutionMode: "remote",
	}

	response, errorValue := client.GenerateLocalRecoveryResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected local recovery response: %v", errorValue)
	}
	if response != "local failure notice" {
		t.Fatalf("expected local recovery response, got %q", response)
	}
	if receivedDocument.ExecutionMode != "device" {
		t.Fatalf("expected device execution mode, got %q", receivedDocument.ExecutionMode)
	}
}

func TestCapabilityLLMClientRecoveryChatUsesAutoThenDeviceExecutionModes(t *testing.T) {
	receivedExecutionModes := []string{}
	requestContext := RequestContext{RequesterPersonID: "person-1", ConversationID: "conversation-1"}
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		var receivedDocument capabilityChatCompletionRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected chat request document to decode: %v", errorValue)
		}
		receivedExecutionModes = append(receivedExecutionModes, receivedDocument.ExecutionMode)
		if receivedDocument.Context == nil || *receivedDocument.Context != requestContext {
			t.Fatalf("expected requester context %+v, got %+v", requestContext, receivedDocument.Context)
		}
		if receivedDocument.ExecutionMode == "auto" {
			return jsonCapabilityResponse(http.StatusBadGateway, "remote unavailable"), nil
		}
		return jsonCapabilityResponse(http.StatusOK, `{"finishReason":"stop","provider":"capabilityLLM","model":"gemma","message":{"role":"assistant","content":"device recovery chat"},"selectedBackend":"device"}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{Endpoint: "http://internkim-capability", HTTPClient: httpClient},
		ModelName:        "gemma",
		ExecutionMode:    "remote",
	}
	responseContext := ContextWithRequestContext(context.Background(), requestContext)
	response, errorValue := client.GenerateRecoveryChatCompletion(responseContext, ChatCompletionRequest{
		Messages: []ChatCompletionMessage{{Role: "user", Content: "hello"}},
	})
	if errorValue != nil || response.Message.Content != "device recovery chat" {
		t.Fatalf("expected device recovery chat, got %+v, %v", response, errorValue)
	}
	if strings.Join(receivedExecutionModes, ",") != "auto,device" {
		t.Fatalf("expected auto then device execution modes, got %+v", receivedExecutionModes)
	}
}

func TestCapabilityLLMClientLocalRecoveryChatUsesDeviceExecutionMode(t *testing.T) {
	var receivedDocument capabilityChatCompletionRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected chat request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"finishReason":"stop","provider":"capabilityLLM","model":"gemma","message":{"role":"assistant","content":"local recovery chat"},"selectedBackend":"device"}`), nil
	}}
	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{Endpoint: "http://internkim-capability", HTTPClient: httpClient},
		ModelName:        "gemma",
		ExecutionMode:    "remote",
	}
	response, errorValue := client.GenerateLocalRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "local recovery chat" {
		t.Fatalf("expected local recovery chat, got %+v, %v", response, errorValue)
	}
	if receivedDocument.ExecutionMode != "device" {
		t.Fatalf("expected device execution mode, got %q", receivedDocument.ExecutionMode)
	}
}

func TestCapabilityLLMClientLocalRecoveryChatRejectsRemoteBackend(t *testing.T) {
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		return jsonCapabilityResponse(http.StatusOK, `{"finishReason":"stop","provider":"example-gateway","model":"remote-model","message":{"role":"assistant","content":"remote recovery chat"},"selectedBackend":"remote"}`), nil
	}}
	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{Endpoint: "http://internkim-capability", HTTPClient: httpClient},
		ModelName:        "gemma",
		ExecutionMode:    "remote",
	}

	_, errorValue := client.GenerateLocalRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue == nil || errorValue.Error() != "device recovery chat returned a non-device backend" {
		t.Fatalf("expected remote backend rejection, got %v", errorValue)
	}
}

func TestRecoveryAttemptContextPreservesDeadlineAndRequester(t *testing.T) {
	expectedDeadline := time.Now().Add(time.Minute)
	requestContext := RequestContext{RequesterPersonID: "person-1", ConversationID: "conversation-1"}
	responseContext := ContextWithRequestContext(context.Background(), requestContext)
	responseContext, cancelResponse := context.WithDeadline(responseContext, expectedDeadline)
	defer cancelResponse()

	recoveryContext, cancelRecovery := recoveryAttemptContext(responseContext)
	defer cancelRecovery()

	actualDeadline, hasDeadline := recoveryContext.Deadline()
	if !hasDeadline || !actualDeadline.Equal(expectedDeadline) {
		t.Fatalf("expected recovery deadline %v, got %v", expectedDeadline, actualDeadline)
	}
	if actualRequestContext := RequestContextFromContext(recoveryContext); actualRequestContext != requestContext {
		t.Fatalf("expected requester context %+v, got %+v", requestContext, actualRequestContext)
	}
}

type fakeCapabilityHTTPClient struct {
	handler func(*http.Request) (*http.Response, error)
}

func (client fakeCapabilityHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client.handler(request)
}

func jsonCapabilityResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func buildTestStructuredResponseRequest() StructuredResponseRequest {
	return StructuredResponseRequest{
		Messages: []Message{
			{
				Role:    "user",
				Content: "say hello",
			},
		},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:               "reply",
			Document:           `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	}
}

func TestCapabilityLLMClientGenerateChatCompletionPrefersRequestModelName(t *testing.T) {
	sentModelNames := []string{}
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		var requestDocument capabilityChatCompletionRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		sentModelNames = append(sentModelNames, requestDocument.Model)
		return jsonCapabilityResponse(http.StatusOK, `{"finishReason":"stop","provider":"capabilityLLM","model":"answered","message":{"role":"assistant","content":"done"}}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName: "configured-model",
	}

	if _, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{
		Messages: []ChatCompletionMessage{{Role: "user", Content: "check"}},
	}); errorValue != nil {
		t.Fatalf("expected configured model completion: %v", errorValue)
	}
	if _, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{
		ModelName: "requested-model",
		Messages:  []ChatCompletionMessage{{Role: "user", Content: "check"}},
	}); errorValue != nil {
		t.Fatalf("expected requested model completion: %v", errorValue)
	}
	if !reflect.DeepEqual(sentModelNames, []string{"configured-model", "requested-model"}) {
		t.Fatalf("expected the request model to win over the client model, got %v", sentModelNames)
	}
}
