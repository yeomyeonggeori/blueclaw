package memory

import (
	"strings"
	"testing"
	"time"
)

func validFact() Fact {
	return Fact{
		FactID:    "fact-1",
		EpisodeID: "episode-1",
		ScopeType: ScopeTypePrivate,
		ScopeID:   "person-1",
		Kind:      FactKindFact,
		Content:   "이샘플 owns the Q3 review",
		ValidFrom: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestValidateFactAcceptsAWellFormedFact(t *testing.T) {
	if errorValue := ValidateFact(validFact()); errorValue != nil {
		t.Fatalf("expected the fact to validate, got %v", errorValue)
	}
}

func TestValidateFactRejectsEachBrokenField(t *testing.T) {
	cases := map[string]func(*Fact){
		"missing id":                  func(fact *Fact) { fact.FactID = " " },
		"unknown scope":               func(fact *Fact) { fact.ScopeType = "conversation" },
		"private without scope id":    func(fact *Fact) { fact.ScopeID = "" },
		"workspace with scope id":     func(fact *Fact) { fact.ScopeType = ScopeTypeWorkspace },
		"unknown kind":                func(fact *Fact) { fact.Kind = "rumour" },
		"empty content":               func(fact *Fact) { fact.Content = "  " },
		"oversized content":           func(fact *Fact) { fact.Content = strings.Repeat("가", FactContentCharacterLimit+1) },
		"missing valid_from":          func(fact *Fact) { fact.ValidFrom = time.Time{} },
		"temporary without expiry":    func(fact *Fact) { fact.Kind = FactKindTemporary },
		"durable fact with an expiry": func(fact *Fact) { fact.ValidUntil = fact.ValidFrom.Add(time.Hour) },
	}
	for name, mutate := range cases {
		fact := validFact()
		mutate(&fact)
		if errorValue := ValidateFact(fact); errorValue == nil {
			t.Fatalf("expected %s to be rejected", name)
		}
	}
}

func TestValidateFactAcceptsATemporaryFactWithExpiry(t *testing.T) {
	fact := validFact()
	fact.Kind = FactKindTemporary
	fact.ValidUntil = fact.ValidFrom.Add(48 * time.Hour)
	if errorValue := ValidateFact(fact); errorValue != nil {
		t.Fatalf("expected the temporary fact to validate, got %v", errorValue)
	}
}

func TestFactIsLiveHonoursSupersedeForgetAndExpiry(t *testing.T) {
	referenceTime := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	live := validFact()
	if !live.IsLive(referenceTime) {
		t.Fatal("expected an untouched fact to be live")
	}
	superseded := validFact()
	superseded.SupersededBy = "fact-2"
	forgotten := validFact()
	forgotten.ForgottenAt = referenceTime
	expired := validFact()
	expired.ValidUntil = referenceTime.Add(-time.Minute)
	for name, fact := range map[string]Fact{"superseded": superseded, "forgotten": forgotten, "expired": expired} {
		if fact.IsLive(referenceTime) {
			t.Fatalf("expected the %s fact to be dead", name)
		}
	}
}

func TestValidateEmbeddingRequiresTheStoredDimensionCount(t *testing.T) {
	if errorValue := ValidateEmbedding(make([]float32, EmbeddingDimensionCount)); errorValue != nil {
		t.Fatalf("expected a %d-dimensional embedding to validate, got %v", EmbeddingDimensionCount, errorValue)
	}
	if errorValue := ValidateEmbedding(make([]float32, 768)); errorValue == nil {
		t.Fatal("expected a 768-dimensional embedding to be rejected")
	}
}

func TestReaderCanReadAppliesScopeRankAndClasses(t *testing.T) {
	reader := Reader{PersonID: "person-1", CircleIDs: []string{"circle-1"}, SecurityLevelRank: 1, GrantedClasses: []string{"finance"}}
	ownPrivate := validFact()
	otherPrivate := validFact()
	otherPrivate.ScopeID = "person-2"
	memberCircle := validFact()
	memberCircle.ScopeType, memberCircle.ScopeID = ScopeTypeCircle, "circle-1"
	strangerCircle := validFact()
	strangerCircle.ScopeType, strangerCircle.ScopeID = ScopeTypeCircle, "circle-9"
	workspace := validFact()
	workspace.ScopeType, workspace.ScopeID = ScopeTypeWorkspace, ""
	tooSecret := validFact()
	tooSecret.ScopeType, tooSecret.ScopeID, tooSecret.SecurityLevelRank = ScopeTypeWorkspace, "", 2
	wrongClass := validFact()
	wrongClass.ScopeType, wrongClass.ScopeID, wrongClass.RequiredClasses = ScopeTypeWorkspace, "", []string{"legal"}
	rightClass := validFact()
	rightClass.ScopeType, rightClass.ScopeID, rightClass.RequiredClasses = ScopeTypeWorkspace, "", []string{"finance"}

	for name, expectation := range map[string]struct {
		fact    Fact
		canRead bool
	}{
		"own private":     {ownPrivate, true},
		"other private":   {otherPrivate, false},
		"member circle":   {memberCircle, true},
		"stranger circle": {strangerCircle, false},
		"workspace":       {workspace, true},
		"above clearance": {tooSecret, false},
		"missing class":   {wrongClass, false},
		"granted class":   {rightClass, true},
	} {
		if reader.CanRead(expectation.fact) != expectation.canRead {
			t.Fatalf("expected %s readable=%v", name, expectation.canRead)
		}
	}
}
