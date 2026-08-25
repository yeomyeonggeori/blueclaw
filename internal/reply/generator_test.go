package reply

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func TestReplyGeneratorRejectsProviderWithoutChatCompletion(t *testing.T) {
	replyProvider := &structuredOnlyReplyProvider{}
	generator := NewGenerator(replyProvider, nil)

	_, errorValue := generator.GenerateReply(context.Background(), "hello")
	if errorValue == nil || errorValue.Error() != "language model provider does not support chat completion" {
		t.Fatalf("expected unavailable chat completion error, got %v", errorValue)
	}
	if replyProvider.structuredCallCount != 0 {
		t.Fatalf("expected missing chat completion not to downgrade to structured generation, got %d calls", replyProvider.structuredCallCount)
	}
}

func TestReplyGeneratorGeneratesChatReplyWithoutTools(t *testing.T) {
	replyProvider := &chatReplyProvider{
		response: model.ChatCompletionResponse{Message: model.ChatCompletionMessage{Role: "assistant", Content: "  hello from chat  "}},
	}
	generator := NewGenerator(replyProvider, nil)

	reply, errorValue := generator.GenerateReply(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected chat reply generation: %v", errorValue)
	}
	if reply != "hello from chat" {
		t.Fatalf("expected trimmed chat reply, got %q", reply)
	}
	if len(replyProvider.request.Tools) != 0 {
		t.Fatalf("expected no chat tools, got %+v", replyProvider.request.Tools)
	}
	if replyProvider.request.SchemaName != "blueclaw_reply" {
		t.Fatalf("expected blueclaw_reply schema name, got %q", replyProvider.request.SchemaName)
	}
	expectedMessages := generator.chatMessages("hello", agentcontract.VisibleContext{}, nil)
	if !reflect.DeepEqual(replyProvider.request.Messages, expectedMessages) {
		t.Fatalf("expected existing reply messages to be preserved, got %+v", replyProvider.request.Messages)
	}
	if replyProvider.structuredCallCount != 0 {
		t.Fatalf("expected chat reply not to call structured generation, got %d calls", replyProvider.structuredCallCount)
	}
}

func TestReplyGeneratorResolvesChatReplyThroughCompleterAccessor(t *testing.T) {
	replyProvider := &chatReplyProvider{
		response: model.ChatCompletionResponse{Message: model.ChatCompletionMessage{Content: "fallback chat"}},
	}
	generator := NewGenerator(chatCompleterAccessorProvider{
		structuredProvider: staticReplyProvider{content: `{"reply":"structured"}`},
		chatCompleter:      replyProvider,
	}, nil)

	reply, errorValue := generator.GenerateReply(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected accessor chat reply generation: %v", errorValue)
	}
	if reply != "fallback chat" {
		t.Fatalf("expected accessor chat reply, got %q", reply)
	}
}

func TestReplyGeneratorRejectsEmptyChatReply(t *testing.T) {
	replyProvider := &chatReplyProvider{
		response: model.ChatCompletionResponse{Message: model.ChatCompletionMessage{Content: "  "}},
	}
	generator := NewGenerator(replyProvider, nil)

	_, errorValue := generator.GenerateReply(context.Background(), "hello")
	if errorValue == nil || errorValue.Error() != "language model reply is empty" {
		t.Fatalf("expected empty chat reply error, got %v", errorValue)
	}
	if replyProvider.structuredCallCount != 0 {
		t.Fatalf("expected empty chat reply not to use structured fallback, got %d calls", replyProvider.structuredCallCount)
	}
}

func TestReplyGeneratorPropagatesChatErrorWithoutStructuredRetry(t *testing.T) {
	chatError := errors.New("chat contract rejected")
	replyProvider := &chatReplyProvider{chatError: chatError}
	generator := NewGenerator(replyProvider, nil)

	_, errorValue := generator.GenerateReply(context.Background(), "hello")
	if !errors.Is(errorValue, chatError) {
		t.Fatalf("expected chat error to propagate, got %v", errorValue)
	}
	if replyProvider.structuredCallCount != 0 {
		t.Fatalf("expected chat contract error not to trigger structured retry, got %d calls", replyProvider.structuredCallCount)
	}
}

func TestReplyGeneratorPreservesChatCancellationContext(t *testing.T) {
	replyProvider := &chatReplyProvider{chatError: context.Canceled}
	generator := NewGenerator(replyProvider, nil)
	responseContext, cancel := context.WithCancel(context.Background())
	cancel()

	_, errorValue := generator.GenerateReply(responseContext, "hello")
	if !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected cancellation to propagate, got %v", errorValue)
	}
	if replyProvider.responseContext.Err() != context.Canceled {
		t.Fatalf("expected canceled context to reach chat completer, got %v", replyProvider.responseContext.Err())
	}
}

func TestReplyGeneratorInjectsMemoryIntoChatReplyRequest(t *testing.T) {
	replyProvider := &capturingReplyProvider{content: "remembered"}
	generator := NewGenerator(replyProvider, nil)

	_, errorValue := generator.generateReplyWithMemory(
		context.Background(),
		"what did I ask for last time?",
		[]agentcontract.MemoryFact{
			{
				Content: "the user asked for help debugging Mattermost DM replies.",
			},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected reply generation: %v", errorValue)
	}

	body := joinChatMessageContent(replyProvider.request.Messages)
	if len(replyProvider.request.Messages) != 3 {
		t.Fatalf("expected system, flattened context, user messages, got %d", len(replyProvider.request.Messages))
	}
	if !strings.Contains(body, "Runtime:") || !strings.Contains(body, "This week:") {
		t.Fatalf("expected runtime context to be injected, got %q", body)
	}
	if !strings.Contains(body, "debugging Mattermost DM replies") {
		t.Fatalf("expected memory context to be injected, got %q", body)
	}
}

func TestReplyGeneratorInjectsCompactAttributedMemorySummary(t *testing.T) {
	replyProvider := &capturingReplyProvider{content: "remembered"}
	generator := NewGenerator(replyProvider, nil)
	longContent := strings.Repeat("a detailed memory that needs summarizing ", 30) + "RAW_TAIL_SHOULD_NOT_APPEAR"

	_, errorValue := generator.generateReplyWithMemory(
		context.Background(),
		"use what you remember",
		[]agentcontract.MemoryFact{
			{
				ScopeType:       agentcontract.MemoryScopeWorkspace,
				Content:         longContent,
				Score:           0.87,
				SourceEpisodeID: "episode-1",
				ValidAt:         time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected reply generation: %v", errorValue)
	}

	body := joinChatMessageContent(replyProvider.request.Messages)
	if !strings.Contains(body, "Relevant memory") {
		t.Fatalf("expected compact memory heading, got %q", body)
	}
	if !strings.Contains(body, "score=0.87") || !strings.Contains(body, "source=episode-1") {
		t.Fatalf("expected memory attribution, got %q", body)
	}
	if strings.Contains(body, "RAW_TAIL_SHOULD_NOT_APPEAR") {
		t.Fatalf("expected long raw memory content to be compacted, got %q", body)
	}
}

func TestReplyGeneratorPlacesVisibleContextBeforeMemoryAndPrompt(t *testing.T) {
	replyProvider := &capturingReplyProvider{content: "contextual"}
	generator := NewGenerator(replyProvider, nil)

	_, errorValue := generator.GenerateReplyWithContext(
		context.Background(),
		"so what should we do?",
		agentcontract.VisibleContext{
			Messages: []agentcontract.VisibleContextMessage{
				{Speaker: "admin", Text: "let's go with A"},
			},
			HasMoreBefore: true,
			HistoryCursor: "cursor-1",
		},
		[]agentcontract.MemoryFact{
			{Content: "the user prefers a design without redundancy."},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected reply generation: %v", errorValue)
	}

	body := joinChatMessageContent(replyProvider.request.Messages)
	if len(replyProvider.request.Messages) != 3 {
		t.Fatalf("expected system, flattened context, prompt messages, got %d", len(replyProvider.request.Messages))
	}
	visibleIndex := strings.Index(body, "admin: let's go with A")
	memoryIndex := strings.Index(body, "a design without redundancy")
	runtimeIndex := strings.Index(body, "Runtime:")
	promptIndex := strings.LastIndex(body, "so what should we do?")
	if visibleIndex < 0 || memoryIndex < 0 || runtimeIndex < 0 || promptIndex < 0 {
		t.Fatalf("expected visible context, memory, runtime, and prompt, got %q", body)
	}
	if !(visibleIndex < memoryIndex && memoryIndex < runtimeIndex && runtimeIndex < promptIndex) {
		t.Fatalf("expected visible context before memory before the volatile runtime timestamp before the final prompt, got %q", body)
	}
}

type staticReplyProvider struct {
	content string
}

type structuredOnlyReplyProvider struct {
	structuredCallCount int
}

type chatCompleterAccessorProvider struct {
	structuredProvider model.LanguageModelProvider
	chatCompleter      model.ChatCompleter
}

func (replyProvider chatCompleterAccessorProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	return replyProvider.structuredProvider.GenerateResponse(responseContext, prompt)
}

func (replyProvider chatCompleterAccessorProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return replyProvider.structuredProvider.GenerateStructuredResponse(responseContext, structuredResponseRequest)
}

func (replyProvider chatCompleterAccessorProvider) TextChatCompleter() (model.ChatCompleter, bool) {
	return replyProvider.chatCompleter, replyProvider.chatCompleter != nil
}

func (replyProvider staticReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return replyProvider.content, nil
}

func (replyProvider staticReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest model.StructuredResponseRequest) (model.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	return model.StructuredResponse{Content: replyProvider.content}, nil
}

func (replyProvider *structuredOnlyReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return "", nil
}

func (replyProvider *structuredOnlyReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest model.StructuredResponseRequest) (model.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	replyProvider.structuredCallCount++
	return model.StructuredResponse{}, nil
}

type capturingReplyProvider struct {
	content string
	request model.ChatCompletionRequest
}

type chatReplyProvider struct {
	response            model.ChatCompletionResponse
	chatError           error
	request             model.ChatCompletionRequest
	responseContext     context.Context
	structuredCallCount int
}

func (replyProvider *chatReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return "", nil
}

func (replyProvider *chatReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest model.StructuredResponseRequest) (model.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	replyProvider.structuredCallCount++
	return model.StructuredResponse{}, nil
}

func (replyProvider *chatReplyProvider) GenerateChatCompletion(responseContext context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	replyProvider.responseContext = responseContext
	replyProvider.request = request
	return replyProvider.response, replyProvider.chatError
}

func (replyProvider *capturingReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return replyProvider.content, nil
}

func (replyProvider *capturingReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest model.StructuredResponseRequest) (model.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	return model.StructuredResponse{}, errors.New("structured reply generation is not supported")
}

func (replyProvider *capturingReplyProvider) GenerateChatCompletion(responseContext context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	_ = responseContext
	replyProvider.request = request
	return model.ChatCompletionResponse{
		Message: model.ChatCompletionMessage{
			Role:    "assistant",
			Content: replyProvider.content,
		},
	}, nil
}

func joinChatMessageContent(messages []model.ChatCompletionMessage) string {
	content := make([]string, 0, len(messages))
	for _, message := range messages {
		content = append(content, message.Content)
	}
	return strings.Join(content, "\n")
}
