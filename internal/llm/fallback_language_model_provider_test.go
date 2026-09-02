package llm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type correctableStructuredOutputError struct {
	Code                string
	AllowLegacyFallback bool
	Diagnostic          StructuredOutputDiagnostic
}

func (errorValue correctableStructuredOutputError) Error() string {
	return errorValue.Code
}

func (errorValue correctableStructuredOutputError) StructuredOutputCorrection() (StructuredOutputCorrection, bool) {
	if errorValue.AllowLegacyFallback {
		return StructuredOutputCorrection{}, false
	}
	return StructuredOutputCorrection{Code: errorValue.Code, Diagnostic: errorValue.Diagnostic}, true
}

func (errorValue correctableStructuredOutputError) StructuredOutputDiagnostic() (StructuredOutputDiagnostic, bool) {
	return errorValue.Diagnostic, errorValue.Diagnostic.Category != ""
}

type staticLanguageModelProvider struct {
	response                StructuredResponse
	error                   error
	responseCalls           *int
	structuredResponseCalls *int
}

type recoveryChatLanguageModelProvider struct {
	response   ChatCompletionResponse
	error      error
	chatCalls  *int
	localCalls *int
	localReply ChatCompletionResponse
	localError error
}

func (provider recoveryChatLanguageModelProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", provider.error
}

func (provider recoveryChatLanguageModelProvider) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	return StructuredResponse{}, provider.error
}

func (provider recoveryChatLanguageModelProvider) GenerateRecoveryChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	if provider.chatCalls != nil {
		(*provider.chatCalls)++
	}
	return provider.response, provider.error
}

func (provider recoveryChatLanguageModelProvider) GenerateLocalRecoveryChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	if provider.localCalls != nil {
		(*provider.localCalls)++
	}
	return provider.localReply, provider.localError
}

func (staticLanguageModelProvider staticLanguageModelProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	if staticLanguageModelProvider.responseCalls != nil {
		(*staticLanguageModelProvider.responseCalls)++
	}
	if staticLanguageModelProvider.error != nil {
		return "", staticLanguageModelProvider.error
	}

	return staticLanguageModelProvider.response.Content, nil
}

func (staticLanguageModelProvider staticLanguageModelProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	if staticLanguageModelProvider.structuredResponseCalls != nil {
		(*staticLanguageModelProvider.structuredResponseCalls)++
	}
	if staticLanguageModelProvider.error != nil {
		return StructuredResponse{}, staticLanguageModelProvider.error
	}

	return staticLanguageModelProvider.response, nil
}

func TestFallbackLanguageModelProviderUsesFallbackAfterPrimaryFailure(t *testing.T) {
	fallbackLanguageModelProvider := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{
			error: errors.New("primary failed"),
		},
		FallbackProvider: staticLanguageModelProvider{
			response: StructuredResponse{
				ProviderName: "litert-lm",
				ModelName:    "/models/gemma-4-E4B-it.litertlm",
				Content:      `{"answer":"fallback"}`,
			},
		},
	}

	structuredResponse, errorValue := fallbackLanguageModelProvider.GenerateStructuredResponse(
		context.Background(),
		StructuredResponseRequest{
			Messages: []Message{
				{
					Role:    "user",
					Content: "hello",
				},
			},
			StructuredOutputSchema: StructuredOutputSchema{
				Name:               "response",
				Document:           `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected fallback provider to succeed: %v", errorValue)
	}
	if !structuredResponse.UsedFallback {
		t.Fatal("expected fallback provider to mark used fallback")
	}
	if structuredResponse.ProviderName != "litert-lm" {
		t.Fatalf("expected fallback provider name, got %q", structuredResponse.ProviderName)
	}
}

func TestFallbackLanguageModelProviderReturnsCorrectableStructuredErrorWithoutFallback(t *testing.T) {
	var fallbackCalls int
	primaryError := correctableStructuredOutputError{
		Code: "structured_output_invalid",
		Diagnostic: StructuredOutputDiagnostic{
			Category: StructuredOutputDiagnosticSchemaValidation,
			ValidationIssues: []StructuredOutputValidationIssue{{
				FieldPath: "/title",
				Code:      StructuredOutputValidationRequired,
			}},
		},
	}
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{error: primaryError},
		FallbackProvider: staticLanguageModelProvider{
			response:                StructuredResponse{Content: "fallback"},
			structuredResponseCalls: &fallbackCalls,
		},
	}

	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})

	var returnedError correctableStructuredOutputError
	if response.Content != "" || !errors.As(errorValue, &returnedError) || returnedError.Code != primaryError.Code {
		t.Fatalf("expected original correctable error, got %#v and %v", response, errorValue)
	}
	if fallbackCalls != 0 {
		t.Fatalf("expected caller correction before tier fallback, got %d fallback calls", fallbackCalls)
	}
}

func TestFallbackLanguageModelProviderUsesFallbackForEmptyCompletion(t *testing.T) {
	var fallbackCalls int
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{error: correctableStructuredOutputError{
			Code: "structured_output_invalid",
			Diagnostic: StructuredOutputDiagnostic{
				Category:     StructuredOutputDiagnosticEmptyCompletion,
				FinishReason: StructuredOutputDiagnosticFinishStop,
			},
		}},
		FallbackProvider: staticLanguageModelProvider{
			response:                StructuredResponse{Content: "fallback"},
			structuredResponseCalls: &fallbackCalls,
		},
	}

	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})

	if errorValue != nil || response.Content != "fallback" || !response.UsedFallback {
		t.Fatalf("expected an empty completion to fall back to the next tier, got %#v and %v", response, errorValue)
	}
	if fallbackCalls != 1 {
		t.Fatalf("expected one fallback call, got %d", fallbackCalls)
	}
}

func TestFallbackLanguageModelProviderUsesFallbackForNonCorrectableLegacyError(t *testing.T) {
	var fallbackCalls int
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{error: correctableStructuredOutputError{
			Code:                "structured_output_invalid",
			AllowLegacyFallback: true,
			Diagnostic: StructuredOutputDiagnostic{
				Category: StructuredOutputDiagnosticSchemaValidation,
			},
		}},
		FallbackProvider: staticLanguageModelProvider{
			response:                StructuredResponse{Content: "fallback"},
			structuredResponseCalls: &fallbackCalls,
		},
	}

	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})

	if errorValue != nil || response.Content != "fallback" || !response.UsedFallback {
		t.Fatalf("expected existing non-correctable fallback semantics, got %#v and %v", response, errorValue)
	}
	if fallbackCalls != 1 {
		t.Fatalf("expected one fallback call, got %d", fallbackCalls)
	}
}

func TestFallbackLanguageModelProviderUsesRecoveryChatFallbackAfterPrimaryFailure(t *testing.T) {
	primaryCalls := 0
	fallbackCalls := 0
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: recoveryChatLanguageModelProvider{
			error:     errors.New("primary chat failed"),
			chatCalls: &primaryCalls,
		},
		FallbackProvider: recoveryChatLanguageModelProvider{
			response: ChatCompletionResponse{
				FinishReason: "stop",
				Message:      ChatCompletionMessage{Role: "assistant", Content: "fallback recovery chat"},
			},
			chatCalls: &fallbackCalls,
		},
	}

	response, errorValue := provider.GenerateRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "fallback recovery chat" {
		t.Fatalf("expected recovery chat fallback, got %+v, %v", response, errorValue)
	}
	if !response.UsedFallback || primaryCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("expected one primary and fallback call with fallback marker, got response=%+v primary=%d fallback=%d", response, primaryCalls, fallbackCalls)
	}
}

func TestFallbackLanguageModelProviderUsesRecoveryChatFallbackAfterBlankPrimarySuccess(t *testing.T) {
	var logBuffer bytes.Buffer
	primaryCalls := 0
	fallbackCalls := 0
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: recoveryChatLanguageModelProvider{
			response: ChatCompletionResponse{
				FinishReason: "stop",
				Message:      ChatCompletionMessage{Role: "assistant"},
			},
			chatCalls: &primaryCalls,
		},
		FallbackProvider: recoveryChatLanguageModelProvider{
			response: ChatCompletionResponse{
				FinishReason: "stop",
				Message:      ChatCompletionMessage{Role: "assistant", Content: "fallback recovery chat"},
			},
			chatCalls: &fallbackCalls,
		},
		Logger: slog.New(slog.NewTextHandler(&logBuffer, nil)),
	}

	response, errorValue := provider.GenerateRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "fallback recovery chat" {
		t.Fatalf("expected blank primary success to use fallback, got %+v, %v", response, errorValue)
	}
	if !response.UsedFallback || primaryCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("expected one primary and fallback call, got response=%+v primary=%d fallback=%d", response, primaryCalls, fallbackCalls)
	}
	if !strings.Contains(logBuffer.String(), "recovery chat completion is empty") {
		t.Fatalf("expected semantic failure log, got %q", logBuffer.String())
	}
}

func TestFallbackLanguageModelProviderLogsNilErrorSafely(t *testing.T) {
	var logBuffer bytes.Buffer
	provider := FallbackLanguageModelProvider{Logger: slog.New(slog.NewTextHandler(&logBuffer, nil))}

	provider.logFallback("recovery_chat", nil)

	if !strings.Contains(logBuffer.String(), `error="unknown error"`) {
		t.Fatalf("expected nil error to be logged safely, got %q", logBuffer.String())
	}
}

func TestFallbackLanguageModelProviderDoesNotUseRecoveryChatFallbackAfterCancellation(t *testing.T) {
	responseContext, cancel := context.WithCancel(context.Background())
	cancel()
	primaryCalls := 0
	fallbackCalls := 0
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: recoveryChatLanguageModelProvider{
			error:     context.Canceled,
			chatCalls: &primaryCalls,
		},
		FallbackProvider: recoveryChatLanguageModelProvider{
			response: ChatCompletionResponse{
				FinishReason: "stop",
				Message:      ChatCompletionMessage{Role: "assistant", Content: "fallback recovery chat"},
			},
			chatCalls: &fallbackCalls,
		},
	}

	response, errorValue := provider.GenerateRecoveryChatCompletion(responseContext, ChatCompletionRequest{})
	if response.Message.Content != "" || !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected canceled recovery chat without fallback, got %+v and %v", response, errorValue)
	}
	if primaryCalls != 1 || fallbackCalls != 0 {
		t.Fatalf("expected only primary recovery chat call, got primary=%d fallback=%d", primaryCalls, fallbackCalls)
	}
}

func TestFallbackLanguageModelProviderLogsAndDescendsThroughTiers(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelWarn}))

	chain := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{error: errors.New("high tier unavailable")},
		FallbackProvider: FallbackLanguageModelProvider{
			PrimaryProvider:  staticLanguageModelProvider{error: errors.New("medium tier unavailable")},
			FallbackProvider: staticLanguageModelProvider{response: StructuredResponse{ModelName: "low-model"}},
			PrimaryLabel:     "medium",
			FallbackLabel:    "low",
			Logger:           logger,
		},
		PrimaryLabel:  "high",
		FallbackLabel: "medium",
		Logger:        logger,
	}

	structuredResponse, errorValue := chain.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})
	if errorValue != nil {
		t.Fatalf("expected descent to reach a working tier: %v", errorValue)
	}
	if structuredResponse.ModelName != "low-model" {
		t.Fatalf("expected lowest tier to answer, got %q", structuredResponse.ModelName)
	}
	if !structuredResponse.UsedFallback {
		t.Fatal("expected response to be marked as used fallback")
	}

	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, "failedTier=high") || !strings.Contains(logOutput, "failedTier=medium") {
		t.Fatalf("expected a log line per failed tier, got: %s", logOutput)
	}
}

func TestFallbackLanguageModelProviderDoesNotFallbackAfterCancellation(t *testing.T) {
	responseContext, cancel := context.WithCancel(context.Background())
	cancel()
	var responseCalls int
	var structuredResponseCalls int
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{error: context.Canceled},
		FallbackProvider: staticLanguageModelProvider{
			response:                StructuredResponse{Content: "fallback"},
			responseCalls:           &responseCalls,
			structuredResponseCalls: &structuredResponseCalls,
		},
	}

	response, errorValue := provider.GenerateResponse(responseContext, "hello")
	if response != "" || !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected cancellation without fallback response, got %q and %v", response, errorValue)
	}
	structuredResponse, errorValue := provider.GenerateStructuredResponse(responseContext, StructuredResponseRequest{})
	if structuredResponse.Content != "" || !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected cancellation without fallback structured response, got %#v and %v", structuredResponse, errorValue)
	}
	if responseCalls != 0 || structuredResponseCalls != 0 {
		t.Fatalf("expected no fallback calls after cancellation, got response=%d structured=%d", responseCalls, structuredResponseCalls)
	}
}

func TestFallbackLanguageModelProviderDoesNotFallbackAfterDeadline(t *testing.T) {
	responseContext, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	var responseCalls int
	var structuredResponseCalls int
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{error: context.DeadlineExceeded},
		FallbackProvider: staticLanguageModelProvider{
			response:                StructuredResponse{Content: "fallback"},
			responseCalls:           &responseCalls,
			structuredResponseCalls: &structuredResponseCalls,
		},
	}

	response, errorValue := provider.GenerateResponse(responseContext, "hello")
	if response != "" || !errors.Is(errorValue, context.DeadlineExceeded) {
		t.Fatalf("expected deadline without fallback response, got %q and %v", response, errorValue)
	}
	structuredResponse, errorValue := provider.GenerateStructuredResponse(responseContext, StructuredResponseRequest{})
	if structuredResponse.Content != "" || !errors.Is(errorValue, context.DeadlineExceeded) {
		t.Fatalf("expected deadline without fallback structured response, got %#v and %v", structuredResponse, errorValue)
	}
	if responseCalls != 0 || structuredResponseCalls != 0 {
		t.Fatalf("expected no fallback calls after deadline, got response=%d structured=%d", responseCalls, structuredResponseCalls)
	}
}

type chatLanguageModelProvider struct {
	response  ChatCompletionResponse
	error     error
	chatCalls *int
}

func (provider chatLanguageModelProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", provider.error
}

func (provider chatLanguageModelProvider) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	return StructuredResponse{}, provider.error
}

func (provider chatLanguageModelProvider) GenerateChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	if provider.chatCalls != nil {
		(*provider.chatCalls)++
	}
	return provider.response, provider.error
}

func TestFallbackLanguageModelProviderUsesChatFallbackAfterPrimaryFailure(t *testing.T) {
	primaryCalls := 0
	fallbackCalls := 0
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: chatLanguageModelProvider{
			error:     errors.New("chat finish reason contract violated"),
			chatCalls: &primaryCalls,
		},
		FallbackProvider: chatLanguageModelProvider{
			response: ChatCompletionResponse{
				FinishReason: "tool_calls",
				Message:      ChatCompletionMessage{Role: "assistant", Content: "fallback chat"},
			},
			chatCalls: &fallbackCalls,
		},
	}

	response, errorValue := provider.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "fallback chat" {
		t.Fatalf("expected chat fallback, got %+v, %v", response, errorValue)
	}
	if !response.UsedFallback || primaryCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("expected one primary and one fallback chat call, got primary=%d fallback=%d response=%+v", primaryCalls, fallbackCalls, response)
	}
	if response.FallbackReason != "chat finish reason contract violated" {
		t.Fatalf("expected the primary failure reason on the fallback response, got %q", response.FallbackReason)
	}
}

func TestFallbackLanguageModelProviderDoesNotUseChatFallbackAfterCancellation(t *testing.T) {
	primaryCalls := 0
	fallbackCalls := 0
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: chatLanguageModelProvider{
			error:     context.Canceled,
			chatCalls: &primaryCalls,
		},
		FallbackProvider: chatLanguageModelProvider{
			response:  ChatCompletionResponse{Message: ChatCompletionMessage{Role: "assistant", Content: "fallback chat"}},
			chatCalls: &fallbackCalls,
		},
	}
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()

	_, errorValue := provider.GenerateChatCompletion(cancelledContext, ChatCompletionRequest{})
	if errorValue == nil || fallbackCalls != 0 {
		t.Fatalf("expected cancellation to stop without fallback, got error=%v fallbackCalls=%d", errorValue, fallbackCalls)
	}
}

func TestFallbackLanguageModelProviderLaddersAfterProviderCallDeadline(t *testing.T) {
	primaryCalls := 0
	fallbackCalls := 0
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: chatLanguageModelProvider{
			error:     context.DeadlineExceeded,
			chatCalls: &primaryCalls,
		},
		FallbackProvider: chatLanguageModelProvider{
			response: ChatCompletionResponse{
				FinishReason: "stop",
				Message:      ChatCompletionMessage{Role: "assistant", Content: "escalated chat"},
			},
			chatCalls: &fallbackCalls,
		},
	}

	response, errorValue := provider.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "escalated chat" {
		t.Fatalf("expected provider call deadline to escalate to fallback tier, got %+v, %v", response, errorValue)
	}
	if !response.UsedFallback || primaryCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("expected one primary and one fallback call, got primary=%d fallback=%d", primaryCalls, fallbackCalls)
	}
}

func TestFallbackLanguageModelProviderStopsWhenCallerBudgetExpired(t *testing.T) {
	fallbackCalls := 0
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: chatLanguageModelProvider{error: context.DeadlineExceeded},
		FallbackProvider: chatLanguageModelProvider{
			response:  ChatCompletionResponse{Message: ChatCompletionMessage{Role: "assistant", Content: "escalated chat"}},
			chatCalls: &fallbackCalls,
		},
	}
	expiredContext, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, errorValue := provider.GenerateChatCompletion(expiredContext, ChatCompletionRequest{})
	if errorValue == nil || fallbackCalls != 0 {
		t.Fatalf("expected expired caller budget to stop without fallback, got error=%v fallbackCalls=%d", errorValue, fallbackCalls)
	}
}
