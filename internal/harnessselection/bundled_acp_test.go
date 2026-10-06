package harnessselection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/acpharness"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func bundledACP() harnessdriver.ACPFactory {
	return func(acpharness.ToolCatalogPublisher) harnessdriver.Factory { return bundledFactory() }
}

func TestSelectRefusesTheBundledACPHarnessInABuildThatShipsNone(t *testing.T) {
	_, errorValue := Select(config.HarnessConfiguration{Name: BundledACPHarnessName}, publishedCatalog(), SandboxProcessBoundary{})
	if errorValue == nil || !strings.Contains(errorValue.Error(), BundledACPHarnessName) {
		t.Fatalf("expected the build that leaves the loop out to say it has no %q, got %v", BundledACPHarnessName, errorValue)
	}
}

func TestSelectRefusesTheBundledACPHarnessWithoutAPublishedCatalog(t *testing.T) {
	_, errorValue := Select(config.HarnessConfiguration{Name: BundledACPHarnessName}, ToolCatalogEndpoint{}, SandboxProcessBoundary{}, WithBundledACPFactory(bundledACP()))
	if errorValue == nil {
		t.Fatal("expected the agent to be refused without a catalog, because it owns no tools")
	}
}

func TestSelectBuildsTheBundledACPHarnessWithoutAnOsBoundaryItNeedsNoneOf(t *testing.T) {
	selectedFactory, errorValue := Select(config.HarnessConfiguration{Name: BundledACPHarnessName}, publishedCatalog(), SandboxProcessBoundary{}, WithBundledACPFactory(bundledACP()))
	if errorValue != nil || selectedFactory == nil {
		t.Fatalf("the agent runs no process of its own and every tool executes in the host, got %v", errorValue)
	}
}

func TestTheBundledDefaultHarnessStaysTheDefault(t *testing.T) {
	selectedFactory, errorValue := Select(config.HarnessConfiguration{}, publishedCatalog(), SandboxProcessBoundary{}, WithBundledACPFactory(bundledACP()))
	if errorValue != nil || selectedFactory == nil {
		t.Fatalf("an unset harness is still the in-process one, got %v", errorValue)
	}
}

type refusingGate struct{}

func (refusingGate) ReviewToolCall(context.Context, toolcontract.ToolInvocation, toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	return toolcontract.ToolCallReview{Result: toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.PolicyBlocked, "approval", "held")}, nil
}

func TestTheBundledACPHarnessKeepsTheGateTheLauncherPutOnTheToolSet(t *testing.T) {
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })
	publisher := grantingPublisher{endpointURL: "http://127.0.0.1:0/tools", resolver: resolver}
	toolSet := singleToolSet(t)
	toolSet.UseToolCallGate(refusingGate{})

	_, _, revoke, errorValue := publisher.PublishToolCatalog(mcpserver.RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSet})
	if errorValue != nil {
		t.Fatalf("expected a grant: %v", errorValue)
	}
	defer revoke()

	result, _ := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "note_write", Input: json.RawMessage(`{}`)})
	if !result.Failed() {
		t.Fatal("publishing replaced the gate that holds sensitive calls for the requester's approval")
	}
}
