package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluememo"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/taskstate"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

// Qwen3 embedding models expect an instruction on the query side only; the
// capability service adds it in production, this adapter adds it here.
const qwenQueryInstruction = "Instruct: Given a question about a person or their work, retrieve the memory facts that answer it\nQuery: "

type openRouterEmbedder struct {
	client openRouterEmbeddingClient
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
// finished task turns into memories, that a later task corrects an earlier one
// through supersede, and that recall then returns the corrected one. It spends
// a few cents, so it runs only when asked for.
func TestMemoryLiveLLMExtractsCorrectsAndRecalls(t *testing.T) {
	if os.Getenv("BLUECLAW_LIVE_LLM_TEST") != "1" {
		t.Skip("set BLUECLAW_LIVE_LLM_TEST=1 to run the live memory extraction check")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is required for the live memory extraction check")
	}
	modelName := strings.TrimSpace(os.Getenv("BLUECLAW_LIVE_LLM_MODEL"))
	if modelName == "" {
		t.Skip("BLUECLAW_LIVE_LLM_MODEL names the low-tier model the live memory extraction check runs on")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	languageModel, errorValue := openaicompatible.Endpoint{URL: openRouterBaseURL, APIKey: apiKey, ModelName: modelName}.Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	embeddingModelName := "openai/text-embedding-3-small"
	stores := memory.NewStores(t.TempDir(), bluememo.Configuration{
		Embedder: openRouterEmbedder{client: openRouterEmbeddingClient{
			apiKey:     apiKey,
			modelName:  embeddingModelName,
			dimensions: benchmarkEmbeddingDimensionCount,
		}},
		EmbeddingModel: embeddingModelName,
		Model:          memory.LanguageModel{Provider: languageModel},
	})
	t.Cleanup(func() { _ = stores.Close() })
	scope := memory.PersonScope("person-alice")
	store, errorValue := stores.Store(ctx, scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	now := time.Now().UTC()

	rememberTask := func(taskRun agentcontract.TaskRun, steps []taskstate.TaskStep) bluememo.SettleReport {
		t.Helper()
		report, errorValue := stores.Remember(ctx, scope, bluememo.Note{
			GroupID:     taskRun.TaskRunID,
			Body:        memory.RenderTranscript(memory.TaskTranscript(taskRun, steps)),
			SpeakerName: "이샘플",
		})
		if errorValue != nil {
			t.Fatalf("expected %s to settle: %v", taskRun.TaskRunID, errorValue)
		}
		return report
	}

	firstReport := rememberTask(agentcontract.TaskRun{
		TaskRunID:         "live-run-1",
		RequesterPersonID: "person-alice",
		Status:            agentcontract.TaskStatusCompleted,
		Prompt:            "나 이번 주부터 플랫폼 팀으로 옮겼어. 앞으로 회의 요약은 불릿 포인트로 짧게 해줘. 그리고 다음 주 금요일(" + now.Add(9*24*time.Hour).Format("2006-01-02") + ")까지 휴가라서 그날까지는 답장 못 해.",
		Result:            "알겠습니다. 요약은 불릿으로 드리고, 휴가 기간은 기억해 두겠습니다.",
		UpdatedAt:         now,
	}, nil)
	if firstReport.Inserted < 2 {
		t.Fatalf("expected the team move and the preference to be remembered, got %+v", firstReport)
	}
	firstMemories, errorValue := store.Memories(ctx)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	hasExpiry := false
	for _, held := range firstMemories {
		t.Logf("first: static=%v until=%s %s", held.IsStatic, held.ValidUntil.Format(time.DateOnly), held.Content)
		if !held.ValidUntil.IsZero() {
			hasExpiry = true
		}
	}
	if !hasExpiry {
		t.Fatal("expected the vacation to be remembered with the day it ends")
	}

	secondReport := rememberTask(agentcontract.TaskRun{
		TaskRunID:         "live-run-2",
		RequesterPersonID: "person-alice",
		Status:            agentcontract.TaskStatusCompleted,
		Prompt:            "정정할게, 플랫폼 팀이 아니라 데이터 팀으로 옮긴 거야. 요약은 계속 불릿으로 부탁해.",
		Result:            "데이터 팀으로 기억을 고쳤습니다.",
		UpdatedAt:         now.Add(time.Hour),
	}, nil)
	t.Logf("second report: %+v", secondReport)
	if secondReport.Superseded == 0 {
		t.Fatalf("expected the team correction to supersede what it corrects, got %+v", secondReport)
	}

	recall, errorValue := store.Recall(ctx, "이샘플은 지금 어느 팀 소속이야?", memory.DefaultRecallLimit)
	if errorValue != nil {
		t.Fatalf("expected recall to succeed: %v", errorValue)
	}
	if len(recall.Memories) == 0 {
		t.Fatalf("expected recall to answer, got mode=%s reason=%q", recall.Mode, recall.DegradedReason)
	}
	for _, recalled := range recall.Memories {
		t.Logf("recall %.4f: %s", recalled.Score, recalled.Memory.Content)
		if recalled.Memory.SupersededBy != "" {
			t.Fatalf("expected no superseded memory in recall, got %+v", recalled.Memory)
		}
	}
	if !strings.Contains(recall.Memories[0].Memory.Content, "데이터") {
		t.Fatalf("expected the corrected team to rank first, got %q", recall.Memories[0].Memory.Content)
	}

	beforeMundane := len(mustMemories(t, ctx, store))
	rememberTask(agentcontract.TaskRun{
		TaskRunID:         "live-run-3",
		RequesterPersonID: "person-alice",
		Status:            agentcontract.TaskStatusCompleted,
		Prompt:            "이 파일 이름을 report-final.pdf로 바꿔줘",
		Result:            "report.pdf를 report-final.pdf로 바꿨습니다.",
		UpdatedAt:         now.Add(2 * time.Hour),
	}, []taskstate.TaskStep{{Instruction: "continue shell", Status: agentcontract.TaskStatusCompleted, Output: "renamed report.pdf -> report-final.pdf"}})
	if afterMundane := len(mustMemories(t, ctx, store)); afterMundane != beforeMundane {
		t.Fatalf("expected a file rename to leave no memory, got %d more", afterMundane-beforeMundane)
	}

	staticMemories, errorValue := store.Profile(ctx)
	if errorValue != nil {
		t.Fatalf("expected the profile to read: %v", errorValue)
	}
	for _, held := range staticMemories {
		t.Logf("profile: %s", held.Content)
	}
	if len(staticMemories) == 0 {
		t.Fatal("expected what the person is to be kept as static memory")
	}
}

func mustMemories(t *testing.T, ctx context.Context, store *bluememo.Store) []bluememo.Memory {
	t.Helper()
	memories, errorValue := store.Memories(ctx)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return memories
}
