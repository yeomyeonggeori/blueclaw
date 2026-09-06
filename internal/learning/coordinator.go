package learning

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
)

const maxReviewEvidenceBytes = 48 * 1024

type SoulRevision = persona.SoulRevision

type ReviewRecord struct {
	ID       string        `json:"id"`
	Audience string        `json:"audience"`
	Decision Decision      `json:"decision"`
	Traces   []ReviewTrace `json:"traces"`
	Error    string        `json:"error,omitempty"`
}

type reflectionState struct {
	Pending      []Experience `json:"pending"`
	InFlight     []Experience `json:"inFlight,omitempty"`
	ReviewedIDs  []string     `json:"reviewedIDs"`
	LastObserved time.Time    `json:"lastObserved"`
	LastReview   time.Time    `json:"lastReview"`
	ReviewDay    string       `json:"reviewDay"`
	DailyReviews int          `json:"dailyReviews"`
}

type Coordinator struct {
	mutex       sync.Mutex
	store       *Store
	reviewer    Reviewer
	root        string
	state       reflectionState
	tools       func() []string
	reviewing   bool
	activeTasks int
	Report      func(error)
}

func (coordinator *Coordinator) UseAvailableTools(tools func() []string) {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	coordinator.tools = tools
}

func (coordinator *Coordinator) IsEnabled() bool {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	return coordinator.store != nil && coordinator.store.Settings().Enabled
}

func (coordinator *Coordinator) BeginTask() func() {
	coordinator.mutex.Lock()
	coordinator.activeTasks++
	coordinator.mutex.Unlock()
	return func() {
		coordinator.mutex.Lock()
		coordinator.activeTasks--
		coordinator.state.LastObserved = time.Now().UTC()
		coordinator.mutex.Unlock()
	}
}

func NewCoordinator(root string, store *Store, reviewer Reviewer, tools func() []string) (*Coordinator, error) {
	coordinator := &Coordinator{root: root, store: store, reviewer: reviewer, tools: tools}
	document, errorValue := os.ReadFile(coordinator.statePath())
	if os.IsNotExist(errorValue) {
		return coordinator, nil
	}
	if errorValue != nil {
		return nil, errorValue
	}
	if errorValue := json.Unmarshal(document, &coordinator.state); errorValue != nil {
		return nil, errorValue
	}
	if len(coordinator.state.InFlight) > 0 {
		state := coordinator.state
		state.Pending = append(append([]Experience{}, state.InFlight...), state.Pending...)
		state.InFlight = nil
		if errorValue := coordinator.saveState(state); errorValue != nil {
			return nil, errorValue
		}
	}
	return coordinator, nil
}

func (coordinator *Coordinator) Observe(experience Experience) error {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if experience.TaskID == "" || experience.Audience == "" || !json.Valid(experience.Outcome) {
		return errors.New("learning experience is incomplete")
	}
	for _, id := range coordinator.state.ReviewedIDs {
		if id == experience.TaskID {
			return nil
		}
	}
	for _, pending := range coordinator.state.Pending {
		if pending.TaskID == experience.TaskID {
			return nil
		}
	}
	if len(coordinator.state.Pending) >= 100 || len(experience.Request)+len(experience.Outcome) > 32768 {
		return errors.New("learning evidence budget reached")
	}
	state := coordinator.state
	state.Pending = append(append([]Experience{}, state.Pending...), experience)
	state.LastObserved = time.Now().UTC()
	return coordinator.saveState(state)
}

func (coordinator *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if errorValue := coordinator.ReviewPending(ctx, now); errorValue != nil && coordinator.Report != nil {
				coordinator.Report(errorValue)
			}
		}
	}
}

func (coordinator *Coordinator) ReviewPending(ctx context.Context, now time.Time) error {
	batch, errorValue := coordinator.claimBatch(now)
	if errorValue != nil || len(batch) == 0 {
		return errorValue
	}
	defer func() { coordinator.mutex.Lock(); coordinator.reviewing = false; coordinator.mutex.Unlock() }()
	soul, errorValue := persona.ReadSoulDocument(coordinator.root)
	if os.IsNotExist(errorValue) {
		soul = []byte(`{"schemaVersion":1}`)
	} else if errorValue != nil {
		return errorValue
	}
	if _, errorValue := persona.ParseSoul(soul); errorValue != nil {
		return errorValue
	}
	input := ReviewInput{Experience: batch, Soul: soul, Skills: coordinator.store.List(batch[0].Audience, false), ActiveLimit: coordinator.store.Settings().ActiveLimit}
	if coordinator.tools != nil {
		input.AvailableTools = coordinator.tools()
	}
	for _, experience := range batch {
		input.AvailableTools = append(input.AvailableTools, experience.Tools...)
	}
	reviewContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	decision, traces, reviewError := coordinator.reviewer.Review(reviewContext, input)
	record := ReviewRecord{ID: now.UTC().Format("20060102T150405.000000000"), Audience: batch[0].Audience, Decision: decision, Traces: traces}
	if reviewError == nil {
		coordinator.mutex.Lock()
		learningEnabled := coordinator.store != nil && coordinator.store.Settings().Enabled
		foregroundActive := coordinator.activeTasks > 0
		if !learningEnabled || foregroundActive {
			reviewError = errors.New("learning commit cancelled by runtime state")
		} else {
			reviewError = coordinator.applyDecisionLocked(decision, input)
		}
		coordinator.mutex.Unlock()
	}
	if reviewError != nil {
		record.Error = reviewError.Error()
		_ = coordinator.requeueBatch(batch)
	}
	if errorValue := writePrivateJSON(filepath.Join(coordinator.directory(), "reviews", record.ID+".json"), record); errorValue != nil {
		return errorValue
	}
	if reviewError == nil {
		if errorValue := coordinator.completeBatch(batch); errorValue != nil {
			return errorValue
		}
	}
	return reviewError
}

func (coordinator *Coordinator) requeueBatch(batch []Experience) error {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	state := coordinator.state
	state.Pending = append(append([]Experience{}, batch...), state.Pending...)
	state.InFlight = nil
	return coordinator.saveState(state)
}

func (coordinator *Coordinator) completeBatch(batch []Experience) error {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	state := coordinator.state
	state.InFlight = nil
	state.ReviewedIDs = append([]string{}, state.ReviewedIDs...)
	for _, experience := range batch {
		state.ReviewedIDs = append(state.ReviewedIDs, experience.TaskID)
	}
	if len(state.ReviewedIDs) > 2000 {
		state.ReviewedIDs = state.ReviewedIDs[len(state.ReviewedIDs)-2000:]
	}
	return coordinator.saveState(state)
}

func (coordinator *Coordinator) claimBatch(now time.Time) ([]Experience, error) {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if coordinator.store == nil || !coordinator.store.Settings().Enabled {
		return nil, nil
	}
	if coordinator.activeTasks > 0 || coordinator.reviewing || len(coordinator.state.Pending) == 0 {
		return nil, nil
	}
	if now.Sub(coordinator.state.LastObserved) < 5*time.Minute || now.Sub(coordinator.state.LastReview) < time.Hour {
		return nil, nil
	}
	state := coordinator.state
	day := now.UTC().Format("2006-01-02")
	if state.ReviewDay != day {
		state.ReviewDay = day
		state.DailyReviews = 0
	}
	if state.DailyReviews >= 3 {
		return nil, nil
	}
	audience := state.Pending[0].Audience
	batch := []Experience{}
	remaining := []Experience{}
	evidenceBytes := 0
	for _, experience := range state.Pending {
		serializedExperience, errorValue := json.Marshal(experience)
		if errorValue != nil {
			return nil, errorValue
		}
		if experience.Audience == audience && len(batch) < 20 && evidenceBytes+len(serializedExperience) <= maxReviewEvidenceBytes {
			batch = append(batch, experience)
			evidenceBytes += len(serializedExperience)
		} else {
			remaining = append(remaining, experience)
		}
	}
	if len(batch) == 0 {
		return nil, nil
	}
	state.Pending = remaining
	state.InFlight = append([]Experience{}, batch...)
	state.LastReview = now
	state.DailyReviews++
	if errorValue := coordinator.saveState(state); errorValue != nil {
		return nil, errorValue
	}
	coordinator.reviewing = true
	return batch, nil
}

func (coordinator *Coordinator) applyDecisionLocked(decision Decision, input ReviewInput) error {
	if decision.Action == "keep" {
		return nil
	}
	if decision.Action == "soul" {
		return coordinator.applySoulLocked(decision, input.Soul)
	}
	skill := Skill{ID: decision.SkillID, Version: decision.ExpectedVersion, Audience: input.Experience[0].Audience, Description: decision.Description, Instruction: decision.Instruction, EvidenceIDs: decision.EvidenceIDs, Reason: decision.Reason, Verification: "evidence-reviewed", Status: "active"}
	expectedVersion := decision.ExpectedVersion
	if decision.Action == "replace" {
		expectedVersion = decision.ReplaceVersion
	}
	_, errorValue := coordinator.store.ApplyDecision(StoreDecision{Action: decision.Action, Skill: skill, ID: decision.SkillID, ReplaceID: decision.ReplaceID, ExpectedVersion: expectedVersion})
	return errorValue
}

func (coordinator *Coordinator) applySoulLocked(decision Decision, expected json.RawMessage) error {
	document, errorValue := persona.ParseSoul([]byte(decision.SoulDocument))
	if errorValue != nil {
		return errorValue
	}
	canonical, errorValue := persona.CanonicalSoul(document)
	if errorValue != nil {
		return errorValue
	}
	history, errorValue := persona.ReadSoulHistory(coordinator.root)
	if errorValue != nil {
		return errorValue
	}
	current := history[len(history)-1]
	if string(current.Document) != string(expected) {
		return errors.New("soul changed during reflection")
	}
	_, errorValue = persona.AppendSoulRevision(coordinator.root, current.Version, canonical, decision.Reason, decision.EvidenceIDs, "reflection")
	return errorValue
}

func (coordinator *Coordinator) CurrentSoul(context.Context) (SoulRevision, error) {
	history, errorValue := persona.ReadSoulHistory(coordinator.root)
	if errorValue != nil {
		return SoulRevision{}, errorValue
	}
	return history[len(history)-1], nil
}

func (coordinator *Coordinator) SoulHistory(context.Context) ([]SoulRevision, error) {
	return persona.ReadSoulHistory(coordinator.root)
}

func (coordinator *Coordinator) directory() string {
	return filepath.Join(coordinator.root, ".blueclaw", "state", "learning")
}
func (coordinator *Coordinator) statePath() string {
	return filepath.Join(coordinator.directory(), "reflection.json")
}

func (coordinator *Coordinator) saveState(state reflectionState) error {
	if errorValue := writePrivateJSON(coordinator.statePath(), state); errorValue != nil {
		return errorValue
	}
	coordinator.state = state
	return nil
}

func writePrivateJSON(path string, value interface{}) error {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := os.MkdirAll(filepath.Dir(path), 0700); errorValue != nil {
		return errorValue
	}
	file, errorValue := os.CreateTemp(filepath.Dir(path), ".learning-*")
	if errorValue != nil {
		return errorValue
	}
	defer os.Remove(file.Name())
	if _, errorValue := file.Write(document); errorValue != nil {
		file.Close()
		return errorValue
	}
	if errorValue := file.Sync(); errorValue != nil {
		file.Close()
		return errorValue
	}
	if errorValue := file.Close(); errorValue != nil {
		return errorValue
	}
	return os.Rename(file.Name(), path)
}
