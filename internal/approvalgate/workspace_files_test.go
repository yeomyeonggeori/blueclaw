//go:build !nobundledharness

package approvalgate

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestTheFilesACallNamesReachTheResolutionOfItsTarget(t *testing.T) {
	gate, _, taskRun := gateFixture(t)
	gate.UseQuestionWorder(approval.NewWorder(&wordingLanguageModel{question: "가져올까요?"}))
	resolver := resolvedTargetResolver()
	gate.UseApprovalTargetResolver(resolver)
	carried := map[string]any{"workspaceFiles": []any{map[string]any{"workspacePath": "/workspace/inbox/clients.csv"}}}
	ctx := WithWorkspaceFileCarrier(context.Background(), func(context.Context) (map[string]any, *toolcontract.ToolResult) {
		return carried, nil
	})

	if _, errorValue := gate.AwaitApproval(ctx, hintApprovalRequest(taskRun.TaskRunID)); errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}

	if resolver.receivedRequest.Transport["workspaceFiles"] == nil {
		t.Fatalf("a target worked out from a file needs the file, got %+v", resolver.receivedRequest)
	}
}

func TestAFileTheRequesterCannotReadIsTheFailureNotAQuestion(t *testing.T) {
	gate, _, taskRun := gateFixture(t)
	resolver := resolvedTargetResolver()
	gate.UseApprovalTargetResolver(resolver)
	unreadable := toolcontract.ToolFailureResult(toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "read_file", "permission denied: /workspace/private/people/other/clients.csv")
	ctx := WithWorkspaceFileCarrier(context.Background(), func(context.Context) (map[string]any, *toolcontract.ToolResult) {
		return nil, &unreadable
	})

	outcome, errorValue := gate.AwaitApproval(ctx, hintApprovalRequest(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}

	if outcome.Decision != mcpserver.ApprovalDecisionUnresolvedTarget || outcome.Failure.Failure == nil {
		t.Fatalf("an unreadable file is answered to the model, never asked about, got %+v", outcome)
	}
	if resolver.callCount != 0 {
		t.Fatal("nothing is resolved from a file that could not be read")
	}
}
