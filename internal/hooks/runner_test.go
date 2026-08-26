package hooks

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunnerRunsHooksSequentially(t *testing.T) {
	runner := NewRunner()
	runner.RegisterHook(EventPreToolUse, func(_ context.Context, request HookRequest) (HookResult, error) {
		return HookResult{UpdatedToolInput: request.ToolInput.(string) + " one"}, nil
	})
	runner.RegisterHook(EventPreToolUse, func(_ context.Context, request HookRequest) (HookResult, error) {
		return HookResult{UpdatedToolInput: request.ToolInput.(string) + " two"}, nil
	})

	toolInput, errorValue := runner.Run(context.Background(), HookRequest{
		EventName: EventPreToolUse,
		ToolName:  "shell",
		ToolInput: "start",
	})

	if errorValue != nil {
		t.Fatalf("expected hooks to succeed: %v", errorValue)
	}
	if toolInput != "start one two" {
		t.Fatalf("expected sequential hook input updates, got %q", toolInput)
	}
}

func TestRunnerStopsWhenHookBlocks(t *testing.T) {
	runner := NewRunner()
	runner.RegisterHook(EventPreToolUse, func(_ context.Context, request HookRequest) (HookResult, error) {
		return HookResult{UpdatedToolInput: request.ToolInput.(string) + " one"}, nil
	})
	runner.RegisterHook(EventPreToolUse, func(context.Context, HookRequest) (HookResult, error) {
		return HookResult{BlockReason: "blocked by policy"}, nil
	})
	runner.RegisterHook(EventPreToolUse, func(_ context.Context, request HookRequest) (HookResult, error) {
		return HookResult{UpdatedToolInput: request.ToolInput.(string) + " skipped"}, nil
	})

	toolInput, errorValue := runner.Run(context.Background(), HookRequest{
		EventName: EventPreToolUse,
		ToolName:  "shell",
		ToolInput: "start",
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "blocked by policy") {
		t.Fatalf("expected block error, got %v", errorValue)
	}
	if toolInput != "start one" {
		t.Fatalf("expected input before block, got %q", toolInput)
	}
}

func TestRunnerReturnsHookError(t *testing.T) {
	runner := NewRunner()
	runner.RegisterHook(EventPreToolUse, func(context.Context, HookRequest) (HookResult, error) {
		return HookResult{}, errors.New("hook failed")
	})

	_, errorValue := runner.Run(context.Background(), HookRequest{
		EventName: EventPreToolUse,
		ToolName:  "shell",
		ToolInput: "start",
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "hook failed") {
		t.Fatalf("expected hook error, got %v", errorValue)
	}
}
