package llm

import (
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type (
	ChatCompleterAccessor              = model.ChatCompleterAccessor
	RecoveryChatCompleterAccessor      = model.RecoveryChatCompleterAccessor
	LocalRecoveryChatCompleterAccessor = model.LocalRecoveryChatCompleterAccessor
	ChatCompletionRequest              = model.ChatCompletionRequest
	ChatCompletionResponse             = model.ChatCompletionResponse
	ChatCompletionMessage              = model.ChatCompletionMessage
	ChatCompletionTool                 = model.ChatCompletionTool
	ChatCompletionFunction             = model.ChatCompletionFunction
	ChatCompletionToolCall             = model.ChatCompletionToolCall
	ChatCompletionToolCallFunction     = model.ChatCompletionToolCallFunction
	ChatCompleter                      = model.ChatCompleter
	RecoveryChatCompleter              = model.RecoveryChatCompleter
	LocalRecoveryChatCompleter         = model.LocalRecoveryChatCompleter
	Message                            = model.Message
	MessagePart                        = model.MessagePart
	StructuredOutputSchema             = model.StructuredOutputSchema
	GenerationOptions                  = model.GenerationOptions
	StructuredResponseRequest          = model.StructuredResponseRequest
	RequestContext                     = model.RequestContext
	Usage                              = model.Usage
	StructuredResponse                 = model.StructuredResponse
	LanguageModelProvider              = model.LanguageModelProvider
	RecoveryResponder                  = model.RecoveryResponder
	LocalRecoveryResponder             = model.LocalRecoveryResponder
	EmbeddingProvider                  = model.EmbeddingProvider
	StructuredOutputDiagnosticCategory = model.StructuredOutputDiagnosticCategory
	StructuredOutputFinishReason       = model.StructuredOutputFinishReason
	StructuredOutputValidationCode     = model.StructuredOutputValidationCode
	StructuredOutputRepairStatus       = model.StructuredOutputRepairStatus
	StructuredOutputValidationIssue    = model.StructuredOutputValidationIssue
	StructuredOutputDiagnostic         = model.StructuredOutputDiagnostic
	StructuredOutputCorrection         = model.StructuredOutputCorrection
)

const (
	StructuredOutputDiagnosticJSONParse           = model.StructuredOutputDiagnosticJSONParse
	StructuredOutputDiagnosticSchemaValidation    = model.StructuredOutputDiagnosticSchemaValidation
	StructuredOutputDiagnosticFinishReason        = model.StructuredOutputDiagnosticFinishReason
	StructuredOutputDiagnosticEmptyCompletion     = model.StructuredOutputDiagnosticEmptyCompletion
	StructuredOutputDiagnosticToolCallContract    = model.StructuredOutputDiagnosticToolCallContract
	StructuredOutputDiagnosticSerialization       = model.StructuredOutputDiagnosticSerialization
	StructuredOutputDiagnosticFinishStop          = model.StructuredOutputDiagnosticFinishStop
	StructuredOutputDiagnosticFinishLength        = model.StructuredOutputDiagnosticFinishLength
	StructuredOutputDiagnosticFinishToolCalls     = model.StructuredOutputDiagnosticFinishToolCalls
	StructuredOutputDiagnosticFinishContentFilter = model.StructuredOutputDiagnosticFinishContentFilter
	StructuredOutputDiagnosticFinishError         = model.StructuredOutputDiagnosticFinishError
	StructuredOutputDiagnosticFinishOther         = model.StructuredOutputDiagnosticFinishOther
	StructuredOutputDiagnosticFinishUnknown       = model.StructuredOutputDiagnosticFinishUnknown
	StructuredOutputValidationRequired            = model.StructuredOutputValidationRequired
	StructuredOutputValidationAdditionalProperty  = model.StructuredOutputValidationAdditionalProperty
	StructuredOutputValidationType                = model.StructuredOutputValidationType
	StructuredOutputValidationOther               = model.StructuredOutputValidationOther
	StructuredOutputRepairNotAttempted            = model.StructuredOutputRepairNotAttempted
	StructuredOutputRepairFailed                  = model.StructuredOutputRepairFailed
)

var (
	ResolveTextChatCompleter            = model.ResolveTextChatCompleter
	ResolveRecoveryChatCompleter        = model.ResolveRecoveryChatCompleter
	ResolveLocalRecoveryChatCompleter   = model.ResolveLocalRecoveryChatCompleter
	ChatCompletionText                  = model.ChatCompletionText
	RecoveryChatCompletionText          = model.RecoveryChatCompletionText
	ContextWithRequestContext           = model.ContextWithRequestContext
	RequestContextFromContext           = model.RequestContextFromContext
	StructuredOutputCorrectionFromError = model.StructuredOutputCorrectionFromError
	StructuredOutputDiagnosticFromError = model.StructuredOutputDiagnosticFromError
)
