package integration

import (
	"context"
	"github.com/yeomyeonggeori/bluememo"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"

	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

const openRouterEmbeddingsURL = "https://openrouter.ai/api/v1/embeddings"

// Qwen3 embedding models expect an instruction on the query side only; the
// capability service adds it in production, this adapter adds it here.
const qwenQueryInstruction = "Instruct: Given a question about a person or their work, retrieve the memory facts that answer it\nQuery: "

type openRouterEmbedder struct {
	client llm.OpenAIEmbeddingClient
}

func (embedder openRouterEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return embedder.client.GenerateEmbedding(ctx, qwenQueryInstruction+text)
}

func (embedder openRouterEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, 0, len(texts))
	for _, text := range texts {
		embedding, errorValue := embedder.client.GenerateEmbedding(ctx, text)
		if errorValue != nil {
			return nil, errorValue
		}
		embeddings = append(embeddings, embedding)
	}
	return embeddings, nil
}

// Proves, against a real low-tier model and a real embedding model, that a
// finished task turns into facts, that a later task corrects an earlier fact
// through supersede, and that recall then returns the corrected fact. It
// spends a few cents, so it runs only when asked for.
func TestMemoryLiveLLMExtractsCorrectsAndRecalls(t *testing.T) {
	if os.Getenv("BLUECLAW_LIVE_LLM_TEST") != "1" {
		t.Skip("set BLUECLAW_LIVE_LLM_TEST=1 to run the live memory extraction check")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is required for the live memory extraction check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	repository := bluememo.NewInMemoryRepository()
	store := bluememo.Store{
		Facts:    repository,
		Profiles: repository,
		Jobs:     repository,
		Embedder: openRouterEmbedder{client: llm.OpenAIEmbeddingClient{
			Endpoint:   openRouterEmbeddingsURL,
			APIKey:     apiKey,
			ModelName:  bluememo.DefaultEmbeddingModelName,
			Dimensions: bluememo.EmbeddingDimensionCount,
		}},
		EmbeddingModel: bluememo.DefaultEmbeddingModelName,
	}
	languageModel := llm.OpenRouterClient{
		APIKey:       apiKey,
		BaseURL:      llm.DefaultOpenRouterChatCompletionsURL,
		ModelName:    llm.DefaultModelTierNames().Low,
		AttemptCount: 2,
	}
	ingester := bluememo.Ingester{Store: store, Model: memory.LanguageModel{Provider: languageModel}}
	reader := bluememo.NewReader("person-alice", []string{"circle-platform"}, nil, 1, nil)
	now := time.Now().UTC()

	firstTask := taskstate.TaskRun{
		TaskRunID:         "live-run-1",
		RequesterPersonID: "person-alice",
		Status:            taskstate.TaskStatusCompleted,
		Prompt:            "나 이번 주부터 플랫폼 팀으로 옮겼어. 앞으로 회의 요약은 불릿 포인트로 짧게 해줘. 그리고 다음 주 금요일(" + now.Add(9*24*time.Hour).Format("2006-01-02") + ")까지 휴가라서 그날까지는 답장 못 해.",
		Result:            "알겠습니다. 요약은 불릿으로 드리고, 휴가 기간은 기억해 두겠습니다.",
		UpdatedAt:         now,
	}
	first, errorValue := ingester.Ingest(ctx, bluememo.IngestRequest{
		Episode: bluememo.Episode{
			EpisodeID:         "live-episode-1",
			SourceKind:        bluememo.EpisodeSourceKindTaskRun,
			SourceID:          firstTask.TaskRunID,
			RequesterPersonID: "person-alice",
			Content:           bluememo.RenderTranscript(memory.TaskTranscript(firstTask, nil)),
			OccurredAt:        now,
		},
		Reader:        reader,
		RequesterName: "이샘플",
		Label:         bluememo.SecurityLabel{RequiredClasses: []string{}},
	})
	if errorValue != nil {
		t.Fatalf("expected the first extraction to succeed: %v", errorValue)
	}
	kinds := map[string]int{}
	for _, fact := range first.Facts {
		kinds[fact.Kind]++
		t.Logf("first extraction: [%s %s] %s", fact.Kind, fact.ScopeType, fact.Content)
	}
	if len(first.Facts) < 2 || kinds[bluememo.FactKindPreference] == 0 || kinds[bluememo.FactKindTemporary] == 0 {
		t.Fatalf("expected at least a preference and a temporary fact, got kinds=%v", kinds)
	}
	for _, fact := range first.Facts {
		if fact.Kind == bluememo.FactKindTemporary && fact.ValidUntil.IsZero() {
			t.Fatalf("expected the temporary fact to carry its expiry, got %+v", fact)
		}
	}

	secondTask := taskstate.TaskRun{
		TaskRunID:         "live-run-2",
		RequesterPersonID: "person-alice",
		Status:            taskstate.TaskStatusCompleted,
		Prompt:            "정정할게, 플랫폼 팀이 아니라 데이터 팀으로 옮긴 거야. 요약은 계속 불릿으로 부탁해.",
		Result:            "데이터 팀으로 기억을 고쳤습니다.",
		UpdatedAt:         now.Add(time.Hour),
	}
	second, errorValue := ingester.Ingest(ctx, bluememo.IngestRequest{
		Episode: bluememo.Episode{
			EpisodeID:         "live-episode-2",
			SourceKind:        bluememo.EpisodeSourceKindTaskRun,
			SourceID:          secondTask.TaskRunID,
			RequesterPersonID: "person-alice",
			Content:           bluememo.RenderTranscript(memory.TaskTranscript(secondTask, nil)),
			OccurredAt:        now.Add(time.Hour),
		},
		Reader:        reader,
		RequesterName: "이샘플",
		Label:         bluememo.SecurityLabel{RequiredClasses: []string{}},
	})
	if errorValue != nil {
		t.Fatalf("expected the second extraction to succeed: %v", errorValue)
	}
	for _, fact := range second.Facts {
		t.Logf("second extraction: [%s %s] %s", fact.Kind, fact.ScopeType, fact.Content)
	}
	t.Logf("second extraction superseded=%v reinforced=%v candidates=%d", second.SupersededFactIDs, second.ReinforcedFactIDs, second.CandidateCount)
	if second.CandidateCount == 0 {
		t.Fatal("expected the first facts to be offered as candidates")
	}
	if len(second.SupersededFactIDs) == 0 {
		t.Fatalf("expected the team correction to supersede an earlier fact, got %+v", second)
	}
	for _, supersededID := range second.SupersededFactIDs {
		superseded, _ := repository.FindFact(supersededID)
		if superseded.SupersededBy == "" {
			t.Fatalf("expected %s to point at its replacement", supersededID)
		}
	}

	recall, errorValue := store.Recall(ctx, bluememo.RecallRequest{Reader: reader, PersonID: "person-alice", Query: "이샘플은 지금 어느 팀 소속이야?"})
	if errorValue != nil {
		t.Fatalf("expected recall to succeed: %v", errorValue)
	}
	if recall.Mode != bluememo.SearchModeHybrid || len(recall.Facts) == 0 {
		t.Fatalf("expected a hybrid recall with results, got mode=%s reason=%q facts=%d", recall.Mode, recall.DegradedReason, len(recall.Facts))
	}
	for _, scoredFact := range recall.Facts {
		t.Logf("recall %.4f: [%s] %s (episode %s)", scoredFact.Score, scoredFact.Fact.Kind, scoredFact.Fact.Content, scoredFact.Fact.EpisodeID)
		if scoredFact.Fact.SupersededBy != "" {
			t.Fatalf("expected no superseded fact in recall, got %+v", scoredFact.Fact)
		}
	}
	if recall.Facts[0].Fact.EpisodeID != "live-episode-2" {
		t.Fatalf("expected the corrected fact to rank first, got %+v", recall.Facts[0].Fact)
	}

	profile, errorValue := bluememo.ProfileBuilder{Store: store, Model: memory.LanguageModel{Provider: languageModel}}.Rebuild(ctx, "person-alice")
	if errorValue != nil {
		t.Fatalf("expected the profile to build: %v", errorValue)
	}
	t.Logf("profile identity=%v current=%v", profile.IdentityLines, profile.CurrentLines)
	if len(profile.IdentityLines) == 0 {
		t.Fatalf("expected identity lines in the profile, got %+v", profile)
	}
}
