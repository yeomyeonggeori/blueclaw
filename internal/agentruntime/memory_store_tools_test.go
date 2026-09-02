package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

var storeToolNow = time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

type storeToolFixture struct {
	repository *bluememo.InMemoryRepository
	model      *bluememotest.ScriptedModel
	store      *bluememo.Store
	toolSet    *toolcontract.ToolSet
}

func newStoreToolFixture(t *testing.T, allowedTools ...string) storeToolFixture {
	t.Helper()
	repository := bluememo.NewInMemoryRepository()
	scripted := bluememotest.NewScriptedModel()
	store := &bluememo.Store{Facts: repository, Profiles: repository, Jobs: repository, Embedder: &bluememotest.HashEmbedder{}, EmbeddingModel: "test-embed", Now: func() time.Time { return storeToolNow }}
	ingester := &bluememo.Ingester{Store: *store, Model: scripted, Now: func() time.Time { return storeToolNow }}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStore(store, ingester, nil)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, allowedTools)
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-alice",
		RequesterName:     "이샘플",
		Platform:          "mattermost",
		ConversationID:    "channel-platform",
		ActiveCircleID:    "circle-platform",
		PersonAccess:      policy.PersonAccess{PersonID: "person-alice", Circles: []string{"circle-platform"}, SecurityLevelRank: 1},
		MemoryLabel:       bluememo.SecurityLabel{SecurityLevelRank: 1, RequiredClasses: []string{"finance"}},
	})
	return storeToolFixture{repository: repository, model: scripted, store: store, toolSet: toolSet}
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

func seededMemoryStore(t *testing.T, personID string, contents ...string) *bluememo.Store {
	t.Helper()
	repository := bluememo.NewInMemoryRepository()
	episode := bluememo.Episode{EpisodeID: "episode-seed-" + personID, SourceKind: bluememo.EpisodeSourceKindImport, SourceID: "seed-" + personID, RequesterPersonID: personID, Content: "seed", OccurredAt: storeToolNow}
	writes := make([]bluememo.FactWrite, 0, len(contents))
	for index, content := range contents {
		fact := bluememo.Fact{FactID: "fact-seed-" + personID + "-" + string(rune('a'+index)), EpisodeID: episode.EpisodeID, ScopeType: bluememo.ScopeTypePrivate, OwnerPersonID: personID, SubjectPersonID: personID, Kind: bluememo.FactKindFact, Content: content, ValidFrom: storeToolNow}
		writes = append(writes, bluememo.FactWrite{Fact: fact, Embedding: bluememotest.Embed(content)})
	}
	if errorValue := repository.SaveEpisode(context.Background(), bluememo.EpisodeWrite{Episode: episode, Facts: writes}); errorValue != nil {
		t.Fatal(errorValue)
	}
	return &bluememo.Store{Facts: repository, Profiles: repository, Jobs: repository, Embedder: &bluememotest.HashEmbedder{}, Now: func() time.Time { return storeToolNow }}
}

func TestMemoryRememberToolIngestsThroughTheStore(t *testing.T) {
	fixture := newStoreToolFixture(t, "memory_remember")
	fixture.model.Queue(bluememotest.IngestResponse(bluememotest.IngestFact{Content: "이샘플 prefers bullet summaries", Kind: bluememo.FactKindPreference, Scope: bluememo.ScopeTypePrivate, Relation: bluememo.FactRelationNew}))
	result := fixture.invoke(t, "memory_remember", map[string]string{"content": "이샘플 prefers bullet summaries"})
	if result.Failed() {
		t.Fatalf("expected memory_remember to succeed, got %s", result.ContentText())
	}
	var output memoryStoreRememberOutput
	decodeToolResult(t, result, &output)
	if !output.Accepted || output.EpisodeID == "" || len(output.FactIDs) != 1 {
		t.Fatalf("expected a persisted fact, got %+v", output)
	}
	stored, isFound := fixture.repository.FindFact(output.FactIDs[0])
	if !isFound || stored.ScopeType != bluememo.ScopeTypePrivate || stored.SecurityLevelRank != 0 || len(stored.RequiredClasses) != 0 {
		t.Fatalf("expected a private fact keyed by its owner and carrying no label, got %+v", stored)
	}
	if !strings.Contains(fixture.model.LastSubject(), "Requester: 이샘플") || !strings.Contains(fixture.model.LastSubject(), "Active circle: circle-platform") {
		t.Fatalf("expected requester and circle context for the merge model, got %s", fixture.model.LastSubject())
	}
}

func TestMemoryRememberToolSharesWithTheCirclesTheModelNamesAndTheRequesterIsIn(t *testing.T) {
	fixture := newStoreToolFixture(t, "memory_remember")
	fixture.model.Queue(bluememotest.IngestResponse(bluememotest.IngestFact{Content: "The platform standup is at 10:00", Kind: bluememo.FactKindFact, Scope: bluememo.ScopeTypeCircle, CircleIDs: []string{"circle-platform", "circle-sales"}, Relation: bluememo.FactRelationNew}))
	var output memoryStoreRememberOutput
	decodeToolResult(t, fixture.invoke(t, "memory_remember", map[string]string{"content": "The platform standup is at 10:00"}), &output)
	stored, isFound := fixture.repository.FindFact(output.FactIDs[0])
	if !isFound || stored.ScopeType != bluememo.ScopeTypeCircle || strings.Join(stored.CircleIDs, ",") != "circle-platform" {
		t.Fatalf("expected the fact shared with the circle the requester is in and no other, got %+v", stored)
	}
	if !strings.Contains(fixture.model.LastSubject(), "Requester's circles: circle-platform") {
		t.Fatalf("expected the merge model to see the requester's circles, got %s", fixture.model.LastSubject())
	}
}

func TestMemoryRememberToolReportsASupersededFact(t *testing.T) {
	fixture := newStoreToolFixture(t, "memory_remember")
	fixture.model.Queue(bluememotest.IngestResponse(bluememotest.IngestFact{Content: "이샘플 works in the platform team", Kind: bluememo.FactKindIdentity, Scope: bluememo.ScopeTypePrivate, Relation: bluememo.FactRelationNew}))
	var first memoryStoreRememberOutput
	firstResult := fixture.invoke(t, "memory_remember", map[string]string{"content": "이샘플 works in the platform team"})
	if firstResult.Failed() {
		t.Fatalf("expected the first remember to succeed, got %s", firstResult.ContentText())
	}
	decodeToolResult(t, firstResult, &first)
	fixture.model.Queue(bluememotest.IngestResponse(bluememotest.IngestFact{Content: "이샘플 works in the data team", Kind: bluememo.FactKindIdentity, Scope: bluememo.ScopeTypePrivate, Relation: bluememo.FactRelationSupersedes, RelatedFactID: first.FactIDs[0]}))
	var second memoryStoreRememberOutput
	decodeToolResult(t, fixture.invoke(t, "memory_remember", map[string]string{"content": "이샘플 moved to the data team"}), &second)
	if len(second.SupersededFactIDs) != 1 || second.SupersededFactIDs[0] != first.FactIDs[0] {
		t.Fatalf("expected the tool to report the superseded fact, got %+v", second)
	}
}

func TestMemoryRememberToolFailsLoudlyWhenTheMergeModelIsDown(t *testing.T) {
	fixture := newStoreToolFixture(t, "memory_remember")
	result := fixture.invoke(t, "memory_remember", map[string]string{"content": "이샘플 prefers bullet summaries"})
	if !result.Failed() || !strings.Contains(result.ContentText(), "memory ingestion model call failed") {
		t.Fatalf("expected a loud failure naming the model call, got %s", result.ContentText())
	}
	if len(fixture.repository.AllFacts()) != 0 {
		t.Fatal("expected nothing written when the merge fails")
	}
}

func TestMemorySearchSurfacesIDsThatMemoryForgetAccepts(t *testing.T) {
	fixture := newStoreToolFixture(t, "memory_search", "memory_forget")
	seed := bluememo.Episode{EpisodeID: "episode-seed", SourceKind: bluememo.EpisodeSourceKindImport, SourceID: "seed", RequesterPersonID: "person-alice", Content: "seed", OccurredAt: storeToolNow}
	ownFact := bluememo.Fact{FactID: "fact-own", EpisodeID: "episode-seed", ScopeType: bluememo.ScopeTypePrivate, OwnerPersonID: "person-alice", Kind: bluememo.FactKindFact, Content: "이샘플 parks on level 2", ValidFrom: storeToolNow}
	otherFact := bluememo.Fact{FactID: "fact-other", EpisodeID: "episode-seed", ScopeType: bluememo.ScopeTypePrivate, OwnerPersonID: "person-bob", Kind: bluememo.FactKindFact, Content: "박예시 parks on level 3", ValidFrom: storeToolNow}
	if errorValue := fixture.repository.SaveEpisode(context.Background(), bluememo.EpisodeWrite{Episode: seed, Facts: []bluememo.FactWrite{{Fact: ownFact, Embedding: bluememotest.Embed(ownFact.Content)}, {Fact: otherFact, Embedding: bluememotest.Embed(otherFact.Content)}}}); errorValue != nil {
		t.Fatal(errorValue)
	}

	blind := fixture.invoke(t, "memory_forget", map[string]any{"factIDs": []string{"fact-own"}, "reason": "moved desks"})
	if !blind.Failed() || !strings.Contains(blind.ContentText(), "unknown: fact-own") {
		t.Fatalf("expected forget without a prior search to fail closed, got %s", blind.ContentText())
	}

	var search memorySearchToolOutput
	decodeToolResult(t, fixture.invoke(t, "memory_search", map[string]string{"query": "parks on level"}), &search)
	if len(search.Facts) != 1 || search.Facts[0].FactID != "fact-own" || search.SearchStatus != memorySearchComplete {
		t.Fatalf("expected only the reader's own fact from a hybrid search, got %+v", search)
	}

	stranger := fixture.invoke(t, "memory_forget", map[string]any{"factIDs": []string{"fact-other"}, "reason": "not mine"})
	if !stranger.Failed() {
		t.Fatalf("expected an unsurfaced ID to be rejected, got %s", stranger.ContentText())
	}
	var forgotten memoryForgetToolOutput
	decodeToolResult(t, fixture.invoke(t, "memory_forget", map[string]any{"factIDs": []string{"fact-own"}, "reason": "moved desks"}), &forgotten)
	if len(forgotten.ForgottenFactIDs) != 1 || forgotten.ForgottenFactIDs[0] != "fact-own" {
		t.Fatalf("expected the surfaced fact forgotten, got %+v", forgotten)
	}
	stored, _ := fixture.repository.FindFact("fact-own")
	if stored.ForgottenAt.IsZero() || stored.ForgetReason != "moved desks" {
		t.Fatalf("expected a soft forget with its reason, got %+v", stored)
	}
	var again memorySearchToolOutput
	decodeToolResult(t, fixture.invoke(t, "memory_search", map[string]string{"query": "parks on level"}), &again)
	if len(again.Facts) != 0 {
		t.Fatalf("expected the forgotten fact to leave search, got %+v", again.Facts)
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
