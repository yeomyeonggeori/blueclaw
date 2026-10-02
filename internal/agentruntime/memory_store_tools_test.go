package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type storeToolFixture struct {
	stores  *memory.Stores
	toolSet *toolcontract.ToolSet
}

func newStoreToolFixture(t *testing.T, allowedTools ...string) storeToolFixture {
	t.Helper()
	return newStoreToolFixtureWithStores(t, memorytest.Open(t), allowedTools...)
}

func newStoreToolFixtureWithStores(t *testing.T, stores *memory.Stores, allowedTools ...string) storeToolFixture {
	t.Helper()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStores(stores, nil)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, allowedTools)
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-alice",
		RequesterName:     "이샘플",
		Platform:          "mattermost",
		ConversationID:    "channel-platform",
		ActiveCircleID:    "circle-platform",
		PersonAccess:      policy.PersonAccess{PersonID: "person-alice", Circles: []string{"circle-platform"}},
	})
	return storeToolFixture{stores: stores, toolSet: toolSet}
}

func (fixture storeToolFixture) invoke(t *testing.T, toolName string, input any) toolcontract.ToolResult {
	t.Helper()
	result, errorValue := fixture.toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: toolName, Input: toolcontract.MarshalToolInput(input)})
	if errorValue != nil {
		t.Fatalf("%s: %v", toolName, errorValue)
	}
	return result
}

func decodeToolResult(t *testing.T, result toolcontract.ToolResult, target any) {
	t.Helper()
	if errorValue := json.Unmarshal([]byte(result.ContentText()), target); errorValue != nil {
		t.Fatalf("expected a JSON tool result, got %s: %v", result.ContentText(), errorValue)
	}
}

func TestMemoryRememberToolSettlesIntoTheActiveCircleFile(t *testing.T) {
	fixture := newStoreToolFixture(t, "memory_remember")
	result := fixture.invoke(t, "memory_remember", map[string]string{"content": "The platform standup is at 10:00"})
	if result.Failed() {
		t.Fatalf("expected memory_remember to succeed, got %s", result.ContentText())
	}
	var output memoryStoreRememberOutput
	decodeToolResult(t, result, &output)
	if !output.Accepted || output.GroupID == "" || output.Inserted != 1 {
		t.Fatalf("expected one memory settled under its own group, got %+v", output)
	}
	if held := memorytest.Count(t, fixture.stores, memory.CircleScope("circle-platform")); held != 1 {
		t.Fatalf("expected the active circle's file to hold it, got %d", held)
	}
	if held := memorytest.Count(t, fixture.stores, memory.PersonScope("person-alice")); held != 0 {
		t.Fatalf("expected the person's own file untouched, got %d", held)
	}
}

func TestMemoryRememberToolReportsASupersededMemory(t *testing.T) {
	stores := memorytest.OpenCorrecting(t)
	scope := memory.CircleScope("circle-platform")
	memorytest.Remember(t, stores, scope, "이샘플 works in the platform team")
	fixture := newStoreToolFixtureWithStores(t, stores, "memory_remember")
	var output memoryStoreRememberOutput
	decodeToolResult(t, fixture.invoke(t, "memory_remember", map[string]string{"content": "이샘플 works in the data team"}), &output)
	if output.Superseded != 1 {
		t.Fatalf("expected the correction to supersede what it corrects, got %+v", output)
	}
}

type failingModel struct{}

func (failingModel) GenerateStructured(_ context.Context, _ bluememo.StructuredRequest) (string, error) {
	return "", errors.New("the model is down")
}

func TestMemoryRememberToolFailsLoudlyWhenTheModelIsDown(t *testing.T) {
	stores := memory.NewStores(t.TempDir(), bluememo.Configuration{
		Embedder: &bluememotest.HashEmbedder{},
		Model:    failingModel{},
		Judge:    bluememo.DistributionJudge{Chooser: bluememotest.ScriptedChooser{}},
	})
	t.Cleanup(func() { _ = stores.Close() })
	fixture := newStoreToolFixtureWithStores(t, stores, "memory_remember")
	result := fixture.invoke(t, "memory_remember", map[string]string{"content": "이샘플 prefers bullet summaries"})
	if !result.Failed() || !strings.Contains(result.ContentText(), "the model is down") {
		t.Fatalf("expected a loud failure naming the model, got %s", result.ContentText())
	}
	if held := memorytest.Count(t, stores, memory.CircleScope("circle-platform")); held != 0 {
		t.Fatalf("expected nothing settled when the model fails, got %d", held)
	}
}

func TestMemorySearchSurfacesIDsThatMemoryForgetAccepts(t *testing.T) {
	stores := memorytest.Open(t)
	memorytest.Remember(t, stores, memory.PersonScope("person-alice"), "이샘플 parks on level 2")
	memorytest.Remember(t, stores, memory.PersonScope("person-bob"), "박예시 parks on level 3")
	fixture := newStoreToolFixtureWithStores(t, stores, "memory_search", "memory_forget")

	blind := fixture.invoke(t, "memory_forget", map[string]any{"factIDs": []string{"some-memory"}, "reason": "moved desks"})
	if !blind.Failed() || !strings.Contains(blind.ContentText(), "unknown: some-memory") {
		t.Fatalf("expected forget without a prior search to fail closed, got %s", blind.ContentText())
	}

	var search memorySearchToolOutput
	decodeToolResult(t, fixture.invoke(t, "memory_search", map[string]string{"query": "parks on level"}), &search)
	if len(search.Facts) != 1 || !strings.Contains(search.Facts[0].Content, "level 2") {
		t.Fatalf("expected only the files this reader is shown, got %+v", search.Facts)
	}
	if search.SearchStatus != memorySearchComplete {
		t.Fatalf("expected an undegraded search, got %q", search.SearchStatus)
	}
	ownFactID := search.Facts[0].FactID

	var forgotten memoryForgetToolOutput
	decodeToolResult(t, fixture.invoke(t, "memory_forget", map[string]any{"factIDs": []string{ownFactID}, "reason": "moved desks"}), &forgotten)
	if len(forgotten.ForgottenFactIDs) != 1 || forgotten.ForgottenFactIDs[0] != ownFactID {
		t.Fatalf("expected the surfaced memory forgotten, got %+v", forgotten)
	}
	var again memorySearchToolOutput
	decodeToolResult(t, fixture.invoke(t, "memory_search", map[string]string{"query": "parks on level"}), &again)
	for _, fact := range again.Facts {
		if fact.FactID == ownFactID {
			t.Fatalf("expected the forgotten memory to leave search, got %+v", again.Facts)
		}
	}
}

func TestStoreMemoryToolDescriptorsCarryPolicyIdentity(t *testing.T) {
	names := map[string]bool{}
	for _, spec := range localToolDescriptorSpecs {
		if spec.Namespace == "memory" {
			names[spec.Name] = true
			if spec.PolicyResource != "tool:"+spec.Name {
				t.Fatalf("expected %s to carry its policy resource, got %q", spec.Name, spec.PolicyResource)
			}
		}
	}
	for _, expected := range []string{"memory_search", "memory_remember", "memory_forget"} {
		if !names[expected] {
			t.Fatalf("expected a descriptor for %s, got %v", expected, names)
		}
	}
}

// seededPersonMemory gives memory files holding what a test says the person is
// already remembered for.
func seededPersonMemory(t *testing.T, personID string, contents ...string) *memory.Stores {
	t.Helper()
	stores := memorytest.Open(t)
	memorytest.Remember(t, stores, memory.PersonScope(personID), contents...)
	return stores
}
