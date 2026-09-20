package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"testing"
)

func invokePlanTool(t *testing.T, input string) json.RawMessage {
	t.Helper()
	toolRegistry := toolcontract.NewToolSet(nil)
	NewToolCatalogBuilder().registerPlanTool(toolRegistry)
	result, errorValue := toolRegistry.InvokeInternal(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.PlanToolName,
		Input:    json.RawMessage(input),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected success, got %+v", result)
	}
	return result.Output.Data
}

func TestPlanToolEchoesNormalizedPlan(t *testing.T) {
	data := invokePlanTool(t, `{"goal":"  ship   the report ","steps":[{"title":"  gather   data ","status":"done"},{"title":"write summary","status":"in_progress"},{"title":"   ","status":"pending"}]}`)

	var output struct {
		Goal  string                  `json:"goal"`
		Steps []toolcontract.PlanStep `json:"steps"`
	}
	if errorValue := json.Unmarshal(data, &output); errorValue != nil {
		t.Fatal(errorValue)
	}
	if output.Goal != "ship the report" {
		t.Fatalf("expected compacted goal, got %q", output.Goal)
	}
	if len(output.Steps) != 2 {
		t.Fatalf("expected the blank-title step dropped, got %+v", output.Steps)
	}
	if output.Steps[0].Title != "gather data" || output.Steps[0].Status != "done" {
		t.Fatalf("expected normalized first step, got %+v", output.Steps[0])
	}
	if output.Steps[1].Status != "in_progress" {
		t.Fatalf("expected preserved status, got %+v", output.Steps[1])
	}
}

func TestPlanToolCarriesTheLevelTheLoopSizesTheTaskFrom(t *testing.T) {
	data := invokePlanTool(t, `{"goal":"rebuild the deck","level":"high","steps":[{"title":"outline","status":"pending"}]}`)

	var plannedSizing struct {
		Level agentcontract.TaskLevel `json:"level"`
	}
	if errorValue := json.Unmarshal(data, &plannedSizing); errorValue != nil {
		t.Fatal(errorValue)
	}
	if plannedSizing.Level != agentcontract.TaskLevelHigh {
		t.Fatalf("expected the planned level to reach the loop, got %q", plannedSizing.Level)
	}
}

func TestPlanToolDropsALevelItDoesNotRecognize(t *testing.T) {
	data := invokePlanTool(t, `{"goal":"rebuild the deck","steps":[{"title":"outline","status":"pending"}]}`)
	if string(data) != `{"goal":"rebuild the deck","steps":[{"title":"outline","status":"pending"}]}` {
		t.Fatalf("expected an unsized plan to carry no level, got %s", data)
	}
}

func TestPlanToolRejectsUnknownStatusAtTheSchemaBoundary(t *testing.T) {
	toolRegistry := toolcontract.NewToolSet(nil)
	NewToolCatalogBuilder().registerPlanTool(toolRegistry)
	result, errorValue := toolRegistry.InvokeInternal(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.PlanToolName,
		Input:    json.RawMessage(`{"steps":[{"title":"x","status":"weird"}]}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected an input schema failure, got %+v", result)
	}
}

func TestPlanToolAcceptsEmptyStepList(t *testing.T) {
	data := invokePlanTool(t, `{"steps":[]}`)
	if string(data) != `{"steps":[]}` {
		t.Fatalf("expected empty plan echo, got %s", data)
	}
}

func TestPlanToolDescriptorIsRegisteredInKernelPalette(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	provider := newKernelToolProvider(toolCatalogBuilder, toolHandlerContext{
		request: ToolCatalogRequest{HistoryProvider: kernelHistoryProvider{}},
	}, toolcontract.NewToolSet(nil))

	boundTools, errorValue := provider.ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, boundTool := range boundTools {
		if boundTool.Definition.Name != toolcontract.PlanToolName {
			continue
		}
		if boundTool.Definition.SideEffectClass != toolcontract.ToolSideEffectNone {
			t.Fatalf("expected a side-effect-free plan tool, got %+v", boundTool.Definition)
		}
		if boundTool.Definition.Completion.Mode != toolcontract.ToolCompletionNone {
			t.Fatalf("expected completion mode none, got %+v", boundTool.Definition.Completion)
		}
		return
	}
	t.Fatal("expected plan in the kernel palette")
}
