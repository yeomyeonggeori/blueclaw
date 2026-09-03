package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func validTaskAddDescriptor() agentruntime.CapabilityToolDescriptor {
	return agentruntime.CapabilityToolDescriptor{
		Name:              "task_add",
		CanonicalName:     "task_add",
		Namespace:         "task",
		ModelName:         "task_add",
		ModelVisibility:   toolcontract.ToolVisibilityModel,
		Description:       "Create a task.",
		PrivacyClass:      "workspace_task",
		InputSchema:       json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{"result":{}},"additionalProperties":false}`),
		ResultContract: &agentruntime.CapabilityToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
		},
		PolicyResource:  "tool:task_add",
		SideEffectClass: toolcontract.ToolSideEffectWorkspaceWrite,
		Availability:    agentruntime.CapabilityAvailability{State: "ok"},
	}
}

func companyInfoGetDescriptorWithOutputSchema(outputSchema string) agentruntime.CapabilityToolDescriptor {
	return agentruntime.CapabilityToolDescriptor{
		Name:              "company_info_get",
		CanonicalName:     "company_info_get",
		Namespace:         "company",
		ModelName:         "company_info_get",
		ModelVisibility:   toolcontract.ToolVisibilityModel,
		Description:       "Read the company's legal attributes.",
		PrivacyClass:      "company_profile",
		InputSchema:       json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema:      json.RawMessage(outputSchema),
		ResultContract: &agentruntime.CapabilityToolResultContract{
			Schema: json.RawMessage(outputSchema),
		},
		PolicyResource:  "tool:company_info_get",
		SideEffectClass: toolcontract.ToolSideEffectRead,
		Availability:    agentruntime.CapabilityAvailability{State: "ok"},
	}
}

func TestHandleListToolsNamesAQuarantinedProviderAndDropsItsTools(t *testing.T) {
	openOutputSchema := `{"type":"object","properties":{"legalAttributes":{"type":"object","additionalProperties":{"type":"string"}}},"additionalProperties":false}`
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []agentruntime.CapabilityToolDescriptor{
		validTaskAddDescriptor(),
		companyInfoGetDescriptorWithOutputSchema(openOutputSchema),
	})
	handler := ToolInventoryHandler{ToolCatalogBuilder: toolCatalogBuilder}

	responseRecorder := httptest.NewRecorder()
	handler.HandleListTools(responseRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/tools", nil))

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var document ToolInventoryDocument
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &document); errorValue != nil {
		t.Fatal(errorValue)
	}

	if slices.Contains(document.Tools, "task_add") {
		t.Fatalf("expected task_add absent, the open company_info_get schema quarantines the whole capabilityd provider, got %+v", document.Tools)
	}
	if slices.Contains(document.Tools, "company_info_get") {
		t.Fatalf("expected company_info_get absent for its own open schema, got %+v", document.Tools)
	}

	if len(document.QuarantinedProviders) != 1 {
		t.Fatalf("expected exactly one quarantined provider, got %+v", document.QuarantinedProviders)
	}
	quarantinedProvider := document.QuarantinedProviders[0]
	if quarantinedProvider.ProviderID != "capabilityd" {
		t.Fatalf("expected the capabilityd provider named, got %q", quarantinedProvider.ProviderID)
	}
	if quarantinedProvider.Reason == "" {
		t.Fatal("expected a reason naming what bluecollar refused")
	}
}

func TestHandleListToolsRegistersAClosedCatalog(t *testing.T) {
	closedOutputSchema := `{"type":"object","properties":{"legalAttributes":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string"},"value":{"type":"string"}},"required":["label","value"],"additionalProperties":false}}},"additionalProperties":false}`
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []agentruntime.CapabilityToolDescriptor{
		validTaskAddDescriptor(),
		companyInfoGetDescriptorWithOutputSchema(closedOutputSchema),
	})
	handler := ToolInventoryHandler{ToolCatalogBuilder: toolCatalogBuilder}

	responseRecorder := httptest.NewRecorder()
	handler.HandleListTools(responseRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/tools", nil))

	var document ToolInventoryDocument
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !slices.Contains(document.Tools, "task_add") {
		t.Fatalf("expected task_add registered, got %+v", document.Tools)
	}
	if !slices.Contains(document.Tools, "company_info_get") {
		t.Fatalf("expected company_info_get registered, got %+v", document.Tools)
	}
	if len(document.QuarantinedProviders) != 0 {
		t.Fatalf("expected no quarantined provider, got %+v", document.QuarantinedProviders)
	}
}
