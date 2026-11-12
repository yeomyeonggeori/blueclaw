package memory_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestRememberingARunNamesWhoAskedForTheRun(t *testing.T) {
	embedder := &memorytest.RequesterRecordingEmbedder{}
	stores := memorytest.OpenEmbeddingWith(t, embedder)
	extractionContext, _ := json.Marshal(memory.ExtractionContext{RequesterName: "이샘플", RequesterEmail: "sample@example.com", Platform: "virtual"})
	taskRuns := &recordedRun{events: []agentcontract.TaskEvent{{Name: memory.ExtractionContextEventName, Body: string(extractionContext)}}}

	memory.TaskRunTransitionObserver{Stores: stores, TaskRuns: taskRuns}.Observe(agentcontract.TaskRun{
		TaskRunID:         "run-1",
		RequesterPersonID: "person-1",
		Prompt:            "Remember that the weekly review moved to Tuesday",
		Result:            "Noted: the weekly review is on Tuesday at 10:00",
		Status:            agentcontract.TaskStatusCompleted,
	})
	deadline := time.Now().Add(10 * time.Second)
	for !taskRuns.has("memory.extraction_completed") {
		if failure := taskRuns.body("memory.extraction_failed"); failure != "" {
			t.Fatalf("remembering failed: %s", failure)
		}
		if time.Now().After(deadline) {
			t.Fatal("the run was never remembered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	requesters := embedder.Requesters()
	if len(requesters) == 0 {
		t.Fatal("remembering the run reached no model, so nothing shows who it was charged to")
	}
	for _, requester := range requesters {
		if requester.RequesterPersonID != "person-1" || requester.RequesterEmail != "sample@example.com" || requester.Platform != "virtual" {
			t.Fatalf("a call made to remember the run did not name who asked for it: %+v", requester)
		}
	}
}
