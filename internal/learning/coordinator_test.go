package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/model"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type failingLearningModel struct{}

func (failingLearningModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("synthetic reviewer failure")
}

func (failingLearningModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, errors.New("synthetic reviewer failure")
}

type blockingLearningModel struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (model *blockingLearningModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}
func (reviewModel *blockingLearningModel) GenerateStructuredResponse(ctx context.Context, _ model.StructuredResponseRequest) (model.StructuredResponse, error) {
	reviewModel.once.Do(func() { close(reviewModel.entered) })
	select {
	case <-reviewModel.release:
		return model.StructuredResponse{Content: `{"action":"keep","skillID":"","expectedVersion":0,"replaceID":"","replaceVersion":0,"description":"","instruction":"","soulDocument":"","evidenceIDs":[],"reason":"keep"}`}, nil
	case <-ctx.Done():
		return model.StructuredResponse{}, ctx.Err()
	}
}

func TestCoordinatorDisabledLearningMakesNoReviewCall(t *testing.T) {
	root := t.TempDir()
	store, errorValue := Open(filepath.Join(root, "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := store.UpdateSettings(Settings{Enabled: true, ActiveLimit: 20}); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := store.UpdateSettings(Settings{Enabled: false, ActiveLimit: 20}); errorValue != nil {
		t.Fatal(errorValue)
	}
	model := &syntheticReviewerModel{responses: []string{`{}`}}
	coordinator, errorValue := NewCoordinator(root, store, Reviewer{Model: model}, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := coordinator.Observe(Experience{TaskID: "task-1", Audience: "company", Outcome: []byte(`{"ok":true}`)}); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := coordinator.ReviewPending(context.Background(), time.Now().UTC().Add(2*time.Hour)); errorValue != nil {
		t.Fatal(errorValue)
	}
	if model.calls != 0 {
		t.Fatalf("disabled learning called model %d times", model.calls)
	}
}

func TestCoordinatorReviewPersistsEvidenceCursorAcrossRestart(t *testing.T) {
	root := t.TempDir()
	store, errorValue := Open(filepath.Join(root, "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := store.UpdateSettings(Settings{Enabled: true, ActiveLimit: 20}); errorValue != nil {
		t.Fatal(errorValue)
	}
	model := &syntheticReviewerModel{responses: []string{`{"action":"keep","skillID":"","expectedVersion":0,"replaceID":"","replaceVersion":0,"description":"","instruction":"","soulDocument":"","evidenceIDs":[],"reason":"keep"}`}}
	coordinator, errorValue := NewCoordinator(root, store, Reviewer{Model: model}, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := coordinator.Observe(Experience{TaskID: "task-2", Audience: "company", Outcome: []byte(`{"ok":true}`)}); errorValue != nil {
		t.Fatal(errorValue)
	}
	coordinator.state.LastObserved = time.Now().UTC().Add(-6 * time.Minute)
	coordinator.state.LastReview = time.Now().UTC().Add(-2 * time.Hour)
	if errorValue := coordinator.ReviewPending(context.Background(), time.Now().UTC()); errorValue != nil {
		t.Fatal(errorValue)
	}
	restarted, errorValue := NewCoordinator(root, store, Reviewer{Model: model}, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(restarted.state.Pending) != 0 || len(restarted.state.ReviewedIDs) != 1 {
		t.Fatalf("cursor was not persisted: %+v", restarted.state)
	}
}

func TestCoordinatorDoesNotCommitWhenForegroundTaskStartsDuringReview(t *testing.T) {
	root := t.TempDir()
	store, errorValue := Open(filepath.Join(root, "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := store.UpdateSettings(Settings{Enabled: true, ActiveLimit: 20}); errorValue != nil {
		t.Fatal(errorValue)
	}
	model := &blockingLearningModel{entered: make(chan struct{}), release: make(chan struct{})}
	coordinator, errorValue := NewCoordinator(root, store, Reviewer{Model: model}, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	_ = coordinator.Observe(Experience{TaskID: "task-3", Audience: "company", Outcome: []byte(`{"ok":true}`)})
	coordinator.state.LastObserved = time.Now().UTC().Add(-6 * time.Minute)
	coordinator.state.LastReview = time.Now().UTC().Add(-2 * time.Hour)
	done := make(chan error, 1)
	go func() { done <- coordinator.ReviewPending(context.Background(), time.Now().UTC()) }()
	<-model.entered
	finishTask := coordinator.BeginTask()
	close(model.release)
	if errorValue := <-done; errorValue == nil {
		t.Fatal("expected active task cancellation")
	}
	finishTask()
	if len(store.List("company", true)) != 0 || len(coordinator.state.Pending) != 1 {
		t.Fatal("review committed or dropped evidence")
	}
}

func TestCoordinatorRecoversInFlightBatchAfterRestart(t *testing.T) {
	root := t.TempDir()
	store, errorValue := Open(filepath.Join(root, "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := store.UpdateSettings(Settings{Enabled: true, ActiveLimit: 20}); errorValue != nil {
		t.Fatal(errorValue)
	}
	coordinator, errorValue := NewCoordinator(root, store, Reviewer{Model: failingLearningModel{}}, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := coordinator.Observe(Experience{TaskID: "recover-me", Audience: "company", Outcome: []byte(`{"ok":true}`)}); errorValue != nil {
		t.Fatal(errorValue)
	}
	now := time.Now().UTC()
	coordinator.state.LastObserved = now.Add(-6 * time.Minute)
	coordinator.state.LastReview = now.Add(-2 * time.Hour)
	batch, errorValue := coordinator.claimBatch(now)
	if errorValue != nil || len(batch) != 1 {
		t.Fatalf("claim batch: %v %+v", errorValue, batch)
	}
	restarted, errorValue := NewCoordinator(root, store, Reviewer{}, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(restarted.state.InFlight) != 0 || len(restarted.state.Pending) != 1 || len(restarted.state.ReviewedIDs) != 0 {
		t.Fatalf("in-flight batch was not recovered: %+v", restarted.state)
	}
}

func TestCoordinatorFailedReviewPreservesEvidenceAndConsumesAttempt(t *testing.T) {
	root := t.TempDir()
	store, errorValue := Open(filepath.Join(root, "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := store.UpdateSettings(Settings{Enabled: true, ActiveLimit: 20}); errorValue != nil {
		t.Fatal(errorValue)
	}
	coordinator, errorValue := NewCoordinator(root, store, Reviewer{Model: failingLearningModel{}}, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := coordinator.Observe(Experience{TaskID: "failed-review", Audience: "company", Outcome: []byte(`{"ok":true}`)}); errorValue != nil {
		t.Fatal(errorValue)
	}
	now := time.Now().UTC()
	coordinator.state.LastObserved = now.Add(-6 * time.Minute)
	coordinator.state.LastReview = now.Add(-2 * time.Hour)
	if errorValue := coordinator.ReviewPending(context.Background(), now); errorValue == nil {
		t.Fatal("expected reviewer failure")
	}
	if len(coordinator.state.Pending) != 1 || len(coordinator.state.InFlight) != 0 || coordinator.state.DailyReviews != 1 {
		t.Fatalf("failed review state: %+v", coordinator.state)
	}
}

func TestCoordinatorDailyAttemptCapCountsFailedReviews(t *testing.T) {
	root := t.TempDir()
	store, errorValue := Open(filepath.Join(root, "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := store.UpdateSettings(Settings{Enabled: true, ActiveLimit: 20}); errorValue != nil {
		t.Fatal(errorValue)
	}
	coordinator, errorValue := NewCoordinator(root, store, Reviewer{Model: failingLearningModel{}}, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := coordinator.Observe(Experience{TaskID: "cap-review", Audience: "company", Outcome: []byte(`{"ok":true}`)}); errorValue != nil {
		t.Fatal(errorValue)
	}
	now := time.Now().UTC()
	for attempt := 0; attempt < 3; attempt++ {
		coordinator.mutex.Lock()
		coordinator.state.LastObserved = now.Add(-6 * time.Minute)
		coordinator.state.LastReview = now.Add(-2 * time.Hour)
		coordinator.mutex.Unlock()
		if errorValue := coordinator.ReviewPending(context.Background(), now.Add(time.Duration(attempt)*time.Hour)); errorValue == nil {
			t.Fatal("expected reviewer failure")
		}
	}
	coordinator.mutex.Lock()
	if coordinator.state.DailyReviews != 3 || len(coordinator.state.Pending) != 1 {
		t.Fatalf("attempt cap state: %+v", coordinator.state)
	}
	coordinator.state.LastObserved = now.Add(-6 * time.Minute)
	coordinator.state.LastReview = now.Add(-2 * time.Hour)
	coordinator.mutex.Unlock()
	if batch, errorValue := coordinator.claimBatch(now.Add(4 * time.Hour)); errorValue != nil || len(batch) != 0 {
		t.Fatalf("fourth attempt was allowed: %+v %v", batch, errorValue)
	}
}

func TestCoordinatorClaimBatchKeepsSerializedEvidenceWithinBudget(t *testing.T) {
	root := t.TempDir()
	store, errorValue := Open(filepath.Join(root, "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := store.UpdateSettings(Settings{Enabled: true, ActiveLimit: 20}); errorValue != nil {
		t.Fatal(errorValue)
	}
	coordinator, errorValue := NewCoordinator(root, store, Reviewer{}, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for taskNumber := 0; taskNumber < 20; taskNumber++ {
		if errorValue := coordinator.Observe(Experience{
			TaskID:   fmt.Sprintf("task-%d", taskNumber),
			Audience: "company",
			Request:  strings.Repeat("request ", 3000),
			Outcome:  []byte(`{"ok":true}`),
		}); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	now := time.Now().UTC()
	coordinator.state.LastObserved = now.Add(-6 * time.Minute)
	coordinator.state.LastReview = now.Add(-2 * time.Hour)
	batch, errorValue := coordinator.claimBatch(now)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(batch) != 2 || len(coordinator.state.Pending) != 18 {
		t.Fatalf("expected byte-bounded batch of two with eighteen pending, got batch=%d pending=%d", len(batch), len(coordinator.state.Pending))
	}
	serializedBytes := 0
	for _, experience := range batch {
		serializedExperience, errorValue := json.Marshal(experience)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		serializedBytes += len(serializedExperience)
	}
	if serializedBytes > maxReviewEvidenceBytes {
		t.Fatalf("claimed evidence exceeded budget: %d", serializedBytes)
	}
}

func TestCoordinatorRetireUsesExpectedVersion(t *testing.T) {
	root := t.TempDir()
	store, errorValue := Open(filepath.Join(root, "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	created, errorValue := store.Put(Skill{ID: "procedure", Audience: "company", Description: "Sample procedure", Instruction: "Run the sample procedure.", Status: "active"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	coordinator := &Coordinator{root: root, store: store}
	input := ReviewInput{Experience: []Experience{{TaskID: "task-retire", Audience: "company"}}}
	decision := Decision{Action: "retire", SkillID: created.ID, ExpectedVersion: created.Version}
	if errorValue := coordinator.applyDecisionLocked(decision, input); errorValue != nil {
		t.Fatalf("retire with current version failed: %v", errorValue)
	}
	retired, errorValue := store.Get(created.ID, "company", true)
	if errorValue != nil || len(retired) != 1 || retired[0].Status != "retired" {
		t.Fatalf("skill was not retired: %+v %v", retired, errorValue)
	}
	created, errorValue = store.Put(Skill{ID: "active-procedure", Audience: "company", Description: "Another procedure", Instruction: "Run another procedure.", Status: "active"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := coordinator.applyDecisionLocked(Decision{Action: "retire", SkillID: created.ID, ExpectedVersion: created.Version - 1}, input); errorValue == nil {
		t.Fatal("stale retirement succeeded")
	}
	active, errorValue := store.Get(created.ID, "company", true)
	if errorValue != nil || len(active) != 1 || active[0].Status != "active" {
		t.Fatalf("stale retirement changed skill: %+v %v", active, errorValue)
	}
}
