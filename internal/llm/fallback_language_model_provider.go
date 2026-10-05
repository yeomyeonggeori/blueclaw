package llm

import (
	"context"
	"errors"
	"log/slog"
)

type FallbackLanguageModelProvider struct {
	PrimaryProvider  LanguageModelProvider
	FallbackProvider LanguageModelProvider
	PrimaryLabel     string
	FallbackLabel    string
	Logger           *slog.Logger

	FallbackServesTheSameTier bool
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	if fallbackLanguageModelProvider.PrimaryProvider == nil {
		return "", errors.New("primary provider is not configured")
	}

	response, errorValue := fallbackLanguageModelProvider.PrimaryProvider.GenerateResponse(responseContext, prompt)
	if contextError := contextFailure(responseContext, errorValue); contextError != nil {
		return response, contextError
	}
	if errorValue == nil || fallbackLanguageModelProvider.FallbackProvider == nil {
		return response, errorValue
	}

	fallbackLanguageModelProvider.logFallback("text", errorValue)
	return fallbackLanguageModelProvider.FallbackProvider.GenerateResponse(responseContext, prompt)
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	if fallbackLanguageModelProvider.PrimaryProvider == nil {
		return StructuredResponse{}, errors.New("primary provider is not configured")
	}

	structuredResponse, errorValue := fallbackLanguageModelProvider.PrimaryProvider.GenerateStructuredResponse(responseContext, structuredResponseRequest)
	if contextError := contextFailure(responseContext, errorValue); contextError != nil {
		return structuredResponse, contextError
	}
	if errorValue == nil || fallbackLanguageModelProvider.FallbackProvider == nil {
		return structuredResponse, errorValue
	}
	if fallbackLanguageModelProvider.leavesCorrectionToTheCaller(errorValue) {
		return structuredResponse, errorValue
	}

	fallbackLanguageModelProvider.logFallback("structured", errorValue)
	primaryFailureReason := errorValue.Error()
	structuredResponse, errorValue = fallbackLanguageModelProvider.FallbackProvider.GenerateStructuredResponse(responseContext, structuredResponseRequest)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	structuredResponse.UsedFallback = true
	structuredResponse.FallbackReason = primaryFailureReason
	return structuredResponse, nil
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) GenerateChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	primaryCompleter, hasPrimaryCompleter := ResolveTextChatCompleter(fallbackLanguageModelProvider.PrimaryProvider)
	fallbackCompleter, hasFallbackCompleter := ResolveTextChatCompleter(fallbackLanguageModelProvider.FallbackProvider)
	if !hasPrimaryCompleter {
		if !hasFallbackCompleter {
			return ChatCompletionResponse{}, errors.New("primary chat provider is not configured")
		}
		return fallbackCompleter.GenerateChatCompletion(responseContext, request)
	}
	response, errorValue := primaryCompleter.GenerateChatCompletion(responseContext, request)
	if contextError := contextFailure(responseContext, errorValue); contextError != nil {
		return response, contextError
	}
	if errorValue == nil || !hasFallbackCompleter {
		return response, errorValue
	}
	fallbackLanguageModelProvider.logFallback("chat", errorValue)
	fallbackResponse, fallbackError := fallbackCompleter.GenerateChatCompletion(responseContext, request)
	if fallbackError != nil {
		return ChatCompletionResponse{}, fallbackError
	}
	fallbackResponse.UsedFallback = true
	fallbackResponse.FallbackReason = errorValue.Error()
	return fallbackResponse, nil
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) GenerateRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	primaryProvider, isPrimaryProvider := fallbackLanguageModelProvider.PrimaryProvider.(RecoveryChatCompleter)
	if !isPrimaryProvider {
		fallbackProvider, isFallbackProvider := fallbackLanguageModelProvider.FallbackProvider.(RecoveryChatCompleter)
		if isFallbackProvider {
			fallbackResponse, fallbackError := fallbackProvider.GenerateRecoveryChatCompletion(responseContext, request)
			if fallbackError != nil {
				return fallbackResponse, fallbackError
			}
			if _, fallbackError = RecoveryChatCompletionText(fallbackResponse); fallbackError != nil {
				return fallbackResponse, fallbackError
			}
			fallbackResponse.UsedFallback = true
			return fallbackResponse, nil
		}
		return ChatCompletionResponse{}, errors.New("primary recovery chat provider is not configured")
	}
	response, errorValue := primaryProvider.GenerateRecoveryChatCompletion(responseContext, request)
	if contextError := contextFailure(responseContext, errorValue); contextError != nil {
		return response, contextError
	}
	if errorValue == nil {
		_, errorValue = RecoveryChatCompletionText(response)
	}
	if errorValue == nil {
		return response, nil
	}
	if fallbackLanguageModelProvider.FallbackProvider == nil {
		return response, errorValue
	}
	fallbackProvider, isFallbackProvider := fallbackLanguageModelProvider.FallbackProvider.(RecoveryChatCompleter)
	if !isFallbackProvider {
		return response, errorValue
	}
	fallbackLanguageModelProvider.logFallback("recovery_chat", errorValue)
	fallbackResponse, fallbackError := fallbackProvider.GenerateRecoveryChatCompletion(responseContext, request)
	if fallbackError != nil {
		return ChatCompletionResponse{}, fallbackError
	}
	if _, fallbackError = RecoveryChatCompletionText(fallbackResponse); fallbackError != nil {
		return fallbackResponse, fallbackError
	}
	fallbackResponse.UsedFallback = true
	fallbackResponse.FallbackReason = errorValue.Error()
	return fallbackResponse, nil
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) GenerateLocalRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	primaryProvider, isPrimaryProvider := fallbackLanguageModelProvider.PrimaryProvider.(LocalRecoveryChatCompleter)
	if !isPrimaryProvider {
		fallbackProvider, isFallbackProvider := fallbackLanguageModelProvider.FallbackProvider.(LocalRecoveryChatCompleter)
		if isFallbackProvider {
			fallbackResponse, fallbackError := fallbackProvider.GenerateLocalRecoveryChatCompletion(responseContext, request)
			if fallbackError != nil {
				return fallbackResponse, fallbackError
			}
			if _, fallbackError = RecoveryChatCompletionText(fallbackResponse); fallbackError != nil {
				return fallbackResponse, fallbackError
			}
			fallbackResponse.UsedFallback = true
			return fallbackResponse, nil
		}
		return ChatCompletionResponse{}, errors.New("primary local recovery chat provider is not configured")
	}
	response, errorValue := primaryProvider.GenerateLocalRecoveryChatCompletion(responseContext, request)
	if contextError := contextFailure(responseContext, errorValue); contextError != nil {
		return response, contextError
	}
	if errorValue == nil {
		_, errorValue = RecoveryChatCompletionText(response)
	}
	if errorValue == nil {
		return response, nil
	}
	if fallbackLanguageModelProvider.FallbackProvider == nil {
		return response, errorValue
	}
	fallbackProvider, isFallbackProvider := fallbackLanguageModelProvider.FallbackProvider.(LocalRecoveryChatCompleter)
	if !isFallbackProvider {
		return response, errorValue
	}
	fallbackLanguageModelProvider.logFallback("local_recovery_chat", errorValue)
	fallbackResponse, fallbackError := fallbackProvider.GenerateLocalRecoveryChatCompletion(responseContext, request)
	if fallbackError != nil {
		return ChatCompletionResponse{}, fallbackError
	}
	if _, fallbackError = RecoveryChatCompletionText(fallbackResponse); fallbackError != nil {
		return fallbackResponse, fallbackError
	}
	fallbackResponse.UsedFallback = true
	fallbackResponse.FallbackReason = errorValue.Error()
	return fallbackResponse, nil
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) leavesCorrectionToTheCaller(errorValue error) bool {
	if fallbackLanguageModelProvider.FallbackServesTheSameTier {
		return false
	}
	_, isCorrectable := StructuredOutputCorrectionFromError(errorValue)
	return isCorrectable
}

func contextFailure(responseContext context.Context, primaryError error) error {
	if primaryError == nil {
		return nil
	}
	if contextError := responseContext.Err(); contextError != nil {
		return contextError
	}
	if errors.Is(primaryError, context.Canceled) {
		return context.Canceled
	}
	return nil
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) logFallback(callKind string, primaryError error) {
	if fallbackLanguageModelProvider.Logger == nil {
		return
	}
	errorMessage := "unknown error"
	if primaryError != nil {
		errorMessage = primaryError.Error()
	}
	fallbackLanguageModelProvider.Logger.Warn(
		"language model call failed; falling back to alternate tier",
		"callKind", callKind,
		"failedTier", fallbackLanguageModelProvider.PrimaryLabel,
		"fallbackTier", fallbackLanguageModelProvider.FallbackLabel,
		"error", errorMessage,
	)
}
