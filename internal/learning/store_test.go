package learning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStoreCapsActiveSkillsAndRestoresRetiredVersion(t *testing.T) {
	store, errorValue := Open(t.TempDir()+"/skills.json", 1)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	created, errorValue := store.Put(Skill{ID: "skill-1", Audience: "company", Instruction: "Do the work", Status: "active"})
	if errorValue != nil || created.Version != 1 {
		t.Fatalf("create failed: %+v %v", created, errorValue)
	}
	if _, errorValue := store.Put(Skill{ID: "skill-2", Audience: "company", Instruction: "Another procedure", Status: "active"}); errorValue == nil {
		t.Fatal("expected active cap")
	}
	if errorValue := store.Retire("skill-1"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := store.Restore("skill-1"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if active := store.List("company", false); len(active) != 1 || active[0].Status != "active" {
		t.Fatalf("unexpected active list: %+v", active)
	}
}

func TestStoreFailedPersistenceKeepsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.json")
	store, errorValue := Open(path, 2)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.Mkdir(path, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := store.Put(Skill{ID: "skill-1", Audience: "company", Instruction: "procedure"}); errorValue == nil {
		t.Fatal("expected persistence failure")
	}
	if len(store.List("company", true)) != 0 {
		t.Fatal("failed persistence changed memory")
	}
}

func TestStoreRejectsMalformedSkillDocumentWithoutMutation(t *testing.T) {
	store, errorValue := Open(filepath.Join(t.TempDir(), "skills.json"), 2)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := store.Put(Skill{ID: "broken", Audience: "company", Description: "Broken", Instruction: "---\nname: broken\n---\n", Status: "active"}); errorValue == nil {
		t.Fatal("malformed learned document was accepted")
	}
	if len(store.List("company", true)) != 0 {
		t.Fatal("malformed learned document changed store state")
	}
	created, errorValue := store.Put(Skill{ID: "valid", Audience: "company", Instruction: "---\nname: valid\ndescription: A valid procedure.\n---\nUse the verified procedure.", Status: "active"})
	if errorValue != nil || created.Version != 1 {
		t.Fatalf("valid learned document rejected: %+v %v", created, errorValue)
	}
}

func TestStoreRejectsMalformedReplacementAndPersistedDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.json")
	store, errorValue := Open(path, 2)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	created, errorValue := store.Put(Skill{ID: "replaceable", Audience: "company", Description: "A procedure", Instruction: "Use the procedure.", Status: "active"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := store.ApplyDecision(StoreDecision{Action: "replace", ReplaceID: created.ID, ExpectedVersion: created.Version, Skill: Skill{ID: "replacement", Audience: "company", Description: "Broken", Instruction: "---\nname: replacement\n---\n", Status: "active"}}); errorValue == nil {
		t.Fatal("malformed replacement was accepted")
	}
	if skills := store.List("company", true); len(skills) != 1 || skills[0].ID != created.ID {
		t.Fatalf("malformed replacement changed store state: %+v", skills)
	}
	malformed, errorValue := json.Marshal(map[string][]Skill{"bad": {{ID: "bad", Audience: "company", Instruction: "---\nname: bad\n---\n", Status: "active"}}})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(path, malformed, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := Open(path, 2); errorValue == nil {
		t.Fatal("malformed persisted skill was silently accepted")
	}
}

func TestStoreConcurrentActivationRespectsCap(t *testing.T) {
	store, errorValue := Open(filepath.Join(t.TempDir(), "skills.json"), 1)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, id := range []string{"a", "b"} {
		if _, errorValue := store.Put(Skill{ID: id, Audience: "company", Instruction: id}); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	var waitGroup sync.WaitGroup
	successes := 0
	var mutex sync.Mutex
	for _, id := range []string{"a", "b"} {
		waitGroup.Add(1)
		go func(id string) {
			defer waitGroup.Done()
			if _, errorValue := store.Activate(id, 1); errorValue == nil {
				mutex.Lock()
				successes++
				mutex.Unlock()
			}
		}(id)
	}
	waitGroup.Wait()
	if successes != 1 {
		t.Fatalf("expected one activation, got %d", successes)
	}
}

func TestStoreSettingsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.json")
	store, errorValue := Open(path, 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := store.UpdateSettings(Settings{Enabled: false, ActiveLimit: 7}); errorValue != nil {
		t.Fatal(errorValue)
	}
	restarted, errorValue := Open(path, 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	settings := restarted.Settings()
	if settings.Enabled || settings.ActiveLimit != 7 {
		t.Fatalf("settings did not persist: %+v", settings)
	}
}

func TestStoreRejectsOversizedLearnedInstruction(t *testing.T) {
	store, errorValue := Open(filepath.Join(t.TempDir(), "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := store.Put(Skill{ID: "large", Audience: "company", Instruction: string(make([]byte, maximumSkillInstructionBytes+1))}); errorValue == nil {
		t.Fatal("expected instruction size rejection")
	}
}

func TestStoreLimitsCandidates(t *testing.T) {
	store, errorValue := Open(filepath.Join(t.TempDir(), "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for index := 0; index < maximumCandidateSkills; index++ {
		if _, errorValue := store.Put(Skill{ID: "candidate-" + string(rune('a'+index)), Audience: "company", Instruction: "procedure"}); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	if _, errorValue := store.Put(Skill{ID: "candidate-over", Audience: "company", Instruction: "procedure"}); errorValue == nil {
		t.Fatal("expected candidate cap")
	}
}

func TestStoreReplacementKeepsCapacityAndAudience(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		audience  string
		version   int
		isAllowed bool
	}{
		{name: "same audience at capacity", audience: "person:sample", version: 1, isAllowed: true},
		{name: "other audience", audience: "person:another", version: 1},
		{name: "missing revision", audience: "person:sample"},
		{name: "stale revision", audience: "person:sample", version: 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store, errorValue := Open(filepath.Join(t.TempDir(), "skills.json"), 1)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			_, errorValue = store.Put(Skill{ID: "previous", Audience: "person:sample", Instruction: "Previous procedure", Status: "active"})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			_, errorValue = store.ApplyDecision(StoreDecision{Action: "replace", ReplaceID: "previous", ExpectedVersion: scenario.version, Skill: Skill{ID: "replacement", Audience: scenario.audience, Instruction: "Updated procedure", Status: "active"}})
			if (errorValue == nil) != scenario.isAllowed {
				t.Fatalf("replacement allowed=%v: %v", scenario.isAllowed, errorValue)
			}
			if store.ActiveCount() != 1 {
				t.Fatal("replacement changed active capacity")
			}
			previous, errorValue := store.Get("previous", "person:sample", false)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if (previous[0].Status == "retired") != scenario.isAllowed {
				t.Fatal("unexpected retirement")
			}
		})
	}
}

func TestStoreRetireDecisionChecksAudienceAndVersionBeforeMutation(t *testing.T) {
	store, errorValue := Open(filepath.Join(t.TempDir(), "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := store.Put(Skill{ID: "private", Audience: "person:sample", Instruction: "procedure", Status: "active"}); errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, decision := range []StoreDecision{
		{Action: "retire", ID: "private", ExpectedVersion: 1, Skill: Skill{Audience: "person:other"}},
		{Action: "retire", ID: "private", ExpectedVersion: 2, Skill: Skill{Audience: "person:sample"}},
	} {
		if _, errorValue := store.ApplyDecision(decision); errorValue == nil {
			t.Fatal("unauthorized or stale retirement succeeded")
		}
	}
	items, errorValue := store.Get("private", "person:sample", false)
	if errorValue != nil || len(items) != 1 || items[0].Status != "active" {
		t.Fatalf("failed retirement changed the skill: %+v %v", items, errorValue)
	}
	retired, errorValue := store.ApplyDecision(StoreDecision{Action: "retire", ID: "private", ExpectedVersion: 1, Skill: Skill{Audience: "person:sample"}})
	if errorValue != nil || retired.Status != "retired" {
		t.Fatalf("authorized retirement failed: %+v %v", retired, errorValue)
	}
}
