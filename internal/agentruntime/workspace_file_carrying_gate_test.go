//go:build !nobundledharness

package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type carriedFilesReadingGate struct {
	carried map[string]any
	failure *toolcontract.ToolResult
}

func (gate *carriedFilesReadingGate) ReviewToolCall(ctx context.Context, _ toolcontract.ToolInvocation, _ toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	gate.carried, gate.failure = approvalgate.CarriedWorkspaceFiles(ctx)
	return toolcontract.ToolCallReview{MayProceed: true}, nil
}

func fileCarryingGateFixture(t *testing.T) (toolcontract.ToolCallGate, *carriedFilesReadingGate) {
	t.Helper()
	toolCatalogBuilder := capabilityReadTestCatalogBuilder(t)
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{
		{Name: "crm_record_import", InputSchema: json.RawMessage(`{"type":"object","properties":{"attachments":{"type":"array","items":{"type":"string"}}}}`)},
	})
	innerGate := &carriedFilesReadingGate{}
	return toolCatalogBuilder.gateCarryingWorkspaceFiles(ToolCatalogRequest{
		ToolCallGate:      innerGate,
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	}), innerGate
}

func TestTheApprovalOfACallThatNamesAFileIsHandedThatFile(t *testing.T) {
	gate, innerGate := fileCarryingGateFixture(t)

	gate.ReviewToolCall(context.Background(), toolcontract.ToolInvocation{ToolName: "crm_record_import", Input: json.RawMessage(`{"attachments":["/workspace/report.md"]}`)}, toolcontract.ToolDefinition{})

	files, _ := innerGate.carried["workspaceFiles"].([]map[string]any)
	if innerGate.failure != nil || len(files) != 1 || files[0]["contentBase64"] != "cmVwb3J0" {
		t.Fatalf("the question about a file is worked out from the file the call will read, got %+v %+v", innerGate.carried, innerGate.failure)
	}
}

func TestAFileTheRequesterCannotReachComesBackAsTheReadFailure(t *testing.T) {
	gate, innerGate := fileCarryingGateFixture(t)

	gate.ReviewToolCall(context.Background(), toolcontract.ToolInvocation{ToolName: "crm_record_import", Input: json.RawMessage(`{"attachments":["/workspace/missing.csv"]}`)}, toolcontract.ToolDefinition{})

	if innerGate.failure == nil || !strings.Contains(innerGate.failure.Output.Content, "missing.csv") {
		t.Fatalf("the model is told which file it could not have, got %+v", innerGate.failure)
	}
}

func TestACallThatNamesNoFileIsReviewedAsItWas(t *testing.T) {
	gate, innerGate := fileCarryingGateFixture(t)

	gate.ReviewToolCall(context.Background(), toolcontract.ToolInvocation{ToolName: "event_delete", Input: json.RawMessage(`{"eventHint":"x"}`)}, toolcontract.ToolDefinition{})

	if innerGate.carried != nil || innerGate.failure != nil {
		t.Fatalf("only a tool whose schema names attachments carries files, got %+v", innerGate)
	}
}
