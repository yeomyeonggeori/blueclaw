//go:build appliance && llmeval

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluememo"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

type memoryModelExchange struct {
	Request              bluememo.StructuredRequest `json:"request"`
	Response             string                     `json:"response"`
	DurationMilliseconds int64                      `json:"durationMilliseconds"`
	Error                string                     `json:"error,omitempty"`
}

type observedMemoryModel struct {
	model     bluememo.LanguageModel
	exchanges []memoryModelExchange
}

func (model *observedMemoryModel) GenerateStructured(ctx context.Context, request bluememo.StructuredRequest) (string, error) {
	startedAt := time.Now()
	response, errorValue := model.model.GenerateStructured(ctx, request)
	exchange := memoryModelExchange{Request: request, Response: response, DurationMilliseconds: time.Since(startedAt).Milliseconds()}
	if errorValue != nil {
		exchange.Error = errorValue.Error()
	}
	model.exchanges = append(model.exchanges, exchange)
	return response, errorValue
}

func TestBluememoIngestProfileAndReplayLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to run the costed memory evaluation")
	}
	endpoint := os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT")
	socketPath := os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET")
	if endpoint == "" && socketPath == "" {
		t.Fatal("live memory evaluation requires a capability endpoint or socket")
	}
	model := &observedMemoryModel{model: memory.LanguageModel{Provider: liveMemoryModel(t, endpoint, socketPath)}}
	artifactRoot := firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_ARTIFACT_DIR"), filepath.Join("..", "..", ".artifacts", "bluememo-live"))
	if errorValue := os.MkdirAll(artifactRoot, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	directory, errorValue := os.MkdirTemp(artifactRoot, "session-")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { writeMemoryModelEvidence(t, directory, model.exchanges) })
	t.Logf("memory model evidence: %s", directory)
	repository := bluememo.NewInMemoryRepository()
	store := bluememo.Store{Facts: repository, Profiles: repository, Jobs: repository, EmbeddingModel: bluememo.DefaultEmbeddingModelName,
		Embedder: llm.CapabilityEmbeddingClient{CapabilityClient: capability.NewClient(capability.Configuration{Endpoint: endpoint, UnixSocketPath: socketPath}), ModelName: bluememo.DefaultEmbeddingModelName, ExecutionMode: "auto", OutputDimensions: bluememo.EmbeddingDimensionCount}}
	ingester := bluememo.Ingester{Store: store, Model: model}
	reader := bluememo.NewReader("memory-test-reader", nil, nil, 0, nil)
	now := time.Now().UTC()
	request := bluememo.IngestRequest{Reader: reader, RequesterName: "이샘플", Episode: bluememo.Episode{EpisodeID: bluememo.NewIdentifier(), SourceKind: bluememo.EpisodeSourceKindExplicit, SourceID: bluememo.NewIdentifier(), RequesterPersonID: reader.PersonID, Content: "나는 이샘플이야. 회의 안건을 하루 전에 받아보는 것을 선호해. 이 선호를 기억해 줘.", OccurredAt: now}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result, errorValue := ingester.Ingest(ctx, request)
	if errorValue != nil || len(result.Facts) == 0 {
		t.Fatalf("live extraction must persist the explicit preference: %+v (%v)", result, errorValue)
	}
	hasPreference := false
	for _, fact := range result.Facts {
		if fact.OwnerPersonID != reader.PersonID || fact.IsShared() {
			t.Fatalf("private preference escaped its requested scope: %+v", fact)
		}
		if fact.Kind == bluememo.FactKindPreference && fact.SubjectPersonID == reader.PersonID {
			hasPreference = true
		}
	}
	if !hasPreference {
		t.Fatalf("live extraction did not identify the requested personal preference: %+v", result.Facts)
	}
	privateContent := seedUnreadableProfileFact(t, repository, now, reader.PersonID)
	profile, errorValue := (bluememo.ProfileBuilder{Store: store, Model: model}).Rebuild(ctx, reader)
	if errorValue != nil || len(profile.IdentityLines)+len(profile.CurrentLines) == 0 || profile.BuiltFromFactCount != len(result.Facts) {
		t.Fatalf("live profile must derive from the reader's visible facts: %+v (%v)", profile, errorValue)
	}
	for _, exchange := range model.exchanges {
		if strings.Contains(exchange.Request.Subject, privateContent) {
			t.Fatal("another owner's private fact reached the model")
		}
	}
	callCount := len(model.exchanges)
	request.Episode.EpisodeID = bluememo.NewIdentifier()
	replayed, errorValue := ingester.Ingest(ctx, request)
	if errorValue != nil || replayed.EpisodeID != result.EpisodeID || len(model.exchanges) != callCount || len(replayed.Facts) != len(result.Facts) {
		t.Fatalf("live result replay must reuse the canonical receipt: %+v (%v)", replayed, errorValue)
	}
	recall, errorValue := store.Recall(ctx, bluememo.RecallRequest{Reader: reader, Query: "회의 준비 선호"})
	if errorValue != nil || len(recall.ProfileLines()) == 0 || len(recall.Facts) == 0 || recall.Mode != bluememo.SearchModeHybrid {
		t.Fatalf("recall must include the live-generated profile and embedded facts: %+v (%v)", recall, errorValue)
	}
	t.Logf("stored facts: %+v; generated profile: %+v", result.Facts, profile)
}

func seedUnreadableProfileFact(t *testing.T, repository *bluememo.InMemoryRepository, now time.Time, personID string) string {
	t.Helper()
	episode := bluememo.Episode{EpisodeID: bluememo.NewIdentifier(), SourceKind: bluememo.EpisodeSourceKindExplicit, SourceID: bluememo.NewIdentifier(), RequesterPersonID: "memory-test-other", Content: "private source", OccurredAt: now}
	fact := bluememo.Fact{FactID: bluememo.NewIdentifier(), EpisodeID: episode.EpisodeID, OwnerPersonID: episode.RequesterPersonID, SubjectPersonID: personID, Kind: bluememo.FactKindIdentity, Content: "이샘플의 비공개 프로젝트 식별자는 PRIVATE-MEMORY-731이다.", ValidFrom: now}
	if errorValue := repository.SaveEpisode(context.Background(), bluememo.EpisodeWrite{Episode: episode, Facts: []bluememo.FactWrite{{Fact: fact}}}); errorValue != nil {
		t.Fatal(errorValue)
	}
	return fact.Content
}

func writeMemoryModelEvidence(t *testing.T, directory string, exchanges []memoryModelExchange) {
	t.Helper()
	document, errorValue := json.MarshalIndent(exchanges, "", "  ")
	if errorValue != nil {
		t.Error(errorValue)
		return
	}
	if errorValue := os.WriteFile(filepath.Join(directory, "model-exchanges.json"), document, 0600); errorValue != nil {
		t.Error(errorValue)
	}
}
