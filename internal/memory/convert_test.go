package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

// A company of five: everyone is in member, three are in management, and
// management is the only circle that contains another.
func conversionProjection() policy.PolicyProjection {
	access := map[string]policy.PersonAccess{
		"person-1": {PersonID: "person-1", Circles: []string{"member"}, SecurityLevelRank: 10, GrantedClasses: []string{"internal"}},
		"person-2": {PersonID: "person-2", Circles: []string{"member"}, SecurityLevelRank: 10, GrantedClasses: []string{"internal"}},
		"person-3": {PersonID: "person-3", Circles: []string{"member", "management"}, SecurityLevelRank: 100, GrantedClasses: []string{"internal", "executive"}},
		"person-4": {PersonID: "person-4", Circles: []string{"member", "management"}, SecurityLevelRank: 100, GrantedClasses: []string{"internal", "executive"}},
		"person-5": {PersonID: "person-5", Circles: []string{"member"}, SecurityLevelRank: 0},
	}
	return policy.PolicyProjection{PersonAccessByPersonID: access}
}

func legacyFact(factID string, content string) memory.LegacyFact {
	return memory.LegacyFact{
		FactID:        factID,
		OwnerPersonID: "person-1",
		Content:       content,
		OccurredAt:    time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		CreatedAt:     time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
	}
}

func placementFor(t *testing.T, fact memory.LegacyFact) memory.Placement {
	t.Helper()
	placements := memory.PlanConversion([]memory.LegacyFact{fact}, conversionProjection())
	if len(placements) != 1 {
		t.Fatalf("planned %d placements, want 1", len(placements))
	}
	return placements[0]
}

func TestPlanSendsAPrivateFactToItsOwnersFile(t *testing.T) {
	placement := placementFor(t, legacyFact("fact-1", "이샘플 prefers the morning slot."))

	if len(placement.Audience) != 1 || placement.Audience[0] != "person-1" {
		t.Fatalf("audience = %v, want only the owner", placement.Audience)
	}
	want := []memory.Scope{memory.PersonScope("person-1")}
	if !sameScopes(placement.Destinations, want) {
		t.Fatalf("destinations = %v, want %v", placement.Destinations, want)
	}
}

func TestPlanSendsAFactEveryCircleMemberReadsToTheCircleFile(t *testing.T) {
	fact := legacyFact("fact-1", "The all-hands moved to Thursday.")
	fact.CircleIDs = []string{"member"}

	placement := placementFor(t, fact)

	if len(placement.Audience) != 5 {
		t.Fatalf("audience = %v, want all five", placement.Audience)
	}
	want := []memory.Scope{memory.CircleScope("member")}
	if !sameScopes(placement.Destinations, want) {
		t.Fatalf("destinations = %v, want the circle's own file", placement.Destinations)
	}
}

func TestPlanSendsAFactNoCircleExpressesToEachReadersFile(t *testing.T) {
	fact := legacyFact("fact-1", "The headcount plan is frozen until June.")
	fact.CircleIDs = []string{"member"}
	fact.SecurityLevelRank = 100
	fact.RequiredClasses = []string{"internal", "executive"}

	placement := placementFor(t, fact)

	// person-1 owns it and reads it whatever the rank; person-3 and person-4
	// meet the rank and hold both classes. No circle holds exactly those three.
	if len(placement.Audience) != 3 {
		t.Fatalf("audience = %v, want the owner and the two who are cleared", placement.Audience)
	}
	want := []memory.Scope{
		memory.PersonScope("person-1"),
		memory.PersonScope("person-3"),
		memory.PersonScope("person-4"),
	}
	if !sameScopes(placement.Destinations, want) {
		t.Fatalf("destinations = %v, want one file per reader", placement.Destinations)
	}
}

func TestPlanKeepsAnOwnerWhoWouldFailTheirOwnFactsClearance(t *testing.T) {
	fact := legacyFact("fact-1", "최견본 signed the lease.")
	fact.OwnerPersonID = "person-5" // rank 0, no classes
	fact.CircleIDs = []string{"member"}
	fact.SecurityLevelRank = 100
	fact.RequiredClasses = []string{"internal", "executive"}

	placement := placementFor(t, fact)

	for _, personID := range placement.Audience {
		if personID == "person-5" {
			return
		}
	}
	t.Fatalf("audience = %v, want the owner among them: an owner read their own fact whatever its rank", placement.Audience)
}

func TestPlanFollowsCircleContainment(t *testing.T) {
	projection := conversionProjection()
	projection.ContainedCirclesByID = map[string][]string{"management": {"board"}}
	fact := legacyFact("fact-1", "The board approved the raise pool.")
	fact.OwnerPersonID = ""
	fact.CircleIDs = []string{"board"}

	placements := memory.PlanConversion([]memory.LegacyFact{fact}, projection)

	if len(placements[0].Audience) != 2 {
		t.Fatalf("audience = %v, want the two in management, which contains board", placements[0].Audience)
	}
}

func TestConvertCarriesTheSentenceAndRunsTwiceWithoutDuplicating(t *testing.T) {
	stores := memorytest.Open(t)
	ctx := context.Background()
	fact := legacyFact("fact-1", "이샘플 prefers the morning slot.")
	fact.Embedding = []float32{0.1, 0.2, 0.3}
	fact.EmbeddingModel = "the retired model"
	facts := []memory.LegacyFact{fact}

	report, errorValue := stores.Convert(ctx, facts, conversionProjection(), len(fact.Embedding))
	if errorValue != nil {
		t.Fatalf("convert: %v", errorValue)
	}
	if report.Facts != 1 || report.Memories != 1 {
		t.Fatalf("report = %+v, want one fact and one memory", report)
	}

	carried := memorytest.Memories(t, stores, memory.PersonScope("person-1"))
	if len(carried) != 1 {
		t.Fatalf("holds %d memories, want 1", len(carried))
	}
	if carried[0].Content != fact.Content {
		t.Errorf("content = %q, want the sentence it arrived with", carried[0].Content)
	}
	if carried[0].OriginID != "fact-1" {
		t.Errorf("origin = %q, want the fact it came from", carried[0].OriginID)
	}

	again, errorValue := stores.Convert(ctx, facts, conversionProjection(), len(fact.Embedding))
	if errorValue != nil {
		t.Fatalf("second convert: %v", errorValue)
	}
	if again.Memories != 0 || again.AlreadyHeld != 1 {
		t.Fatalf("second report = %+v, want nothing written and one already held", again)
	}
}

func sameScopes(left []memory.Scope, right []memory.Scope) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestConvertRefusesACarriedVectorOfTheWrongWidthBeforeWritingAnything(t *testing.T) {
	stores := memorytest.Open(t)
	ctx := context.Background()
	narrow := legacyFact("fact-1", "이샘플 prefers the morning slot.")
	narrow.Embedding = []float32{0.1, 0.2, 0.3}
	narrow.EmbeddingModel = "the retired model"
	wide := legacyFact("fact-2", "박예시 keeps the quarterly ledger.")
	wide.Embedding = []float32{0.1, 0.2, 0.3, 0.4}
	wide.EmbeddingModel = "the retired model"

	_, errorValue := stores.Convert(ctx, []memory.LegacyFact{narrow, wide}, conversionProjection(), 3)

	if !errors.Is(errorValue, memory.ErrCarriedEmbeddingWidth) {
		t.Fatalf("error = %v, want ErrCarriedEmbeddingWidth", errorValue)
	}
	// The narrow one would have been written first and taught a fresh file
	// that three is the width of that model.
	if held := memorytest.Count(t, stores, memory.PersonScope("person-1")); held != 0 {
		t.Fatalf("holds %d memories, want none written", held)
	}
}

func TestPlanLeavesTheSeededAdministratorOutOfEveryAudience(t *testing.T) {
	projection := conversionProjection()
	projection.PersonAccessByPersonID[policy.SeededAdminPersonID] = policy.PersonAccess{
		PersonID:          policy.SeededAdminPersonID,
		Circles:           []string{"member", "management"},
		SecurityLevelRank: 100,
		GrantedClasses:    []string{"internal", "executive"},
	}
	fact := legacyFact("fact-1", "The headcount plan is frozen until June.")
	fact.CircleIDs = []string{"member"}
	fact.SecurityLevelRank = 100
	fact.RequiredClasses = []string{"internal", "executive"}

	placements := memory.PlanConversion([]memory.LegacyFact{fact}, projection)

	for _, personID := range placements[0].Audience {
		if personID == policy.SeededAdminPersonID {
			t.Fatalf("audience = %v, want the seeded administrator left out", placements[0].Audience)
		}
	}
	for _, destination := range placements[0].Destinations {
		if destination.ID == policy.SeededAdminPersonID {
			t.Fatalf("destinations = %v, want no file for the seeded administrator", placements[0].Destinations)
		}
	}
}

func TestPlanOwnsUpToAFactNobodyReads(t *testing.T) {
	projection := conversionProjection()
	fact := legacyFact("fact-1", "이샘플 prefers the morning slot.")
	fact.OwnerPersonID = policy.SeededAdminPersonID
	fact.CircleIDs = []string{"a-circle-nobody-is-in"}

	placements := memory.PlanConversion([]memory.LegacyFact{fact}, projection)

	if len(placements[0].Audience) != 0 || len(placements[0].Destinations) != 0 {
		t.Fatalf("placement = %+v, want no reader and no destination", placements[0])
	}
	if placements[0].Boundary != "nobody reads it" {
		t.Errorf("boundary = %q, want it said out loud", placements[0].Boundary)
	}
}

func TestConvertCountsAFactNobodyReadsRatherThanDroppingItQuietly(t *testing.T) {
	stores := memorytest.Open(t)
	fact := legacyFact("fact-1", "이샘플 prefers the morning slot.")
	fact.OwnerPersonID = policy.SeededAdminPersonID
	fact.CircleIDs = []string{"a-circle-nobody-is-in"}

	report, errorValue := stores.Convert(context.Background(), []memory.LegacyFact{fact}, conversionProjection(), 0)

	if errorValue != nil {
		t.Fatalf("convert: %v", errorValue)
	}
	if report.Unread != 1 || report.Memories != 0 {
		t.Fatalf("report = %+v, want one fact reported as read by nobody", report)
	}
}

// Narrowing the roster is easy to half-do. Leaving the seeded administrator
// out of an audience but not out of a circle's membership makes the two stop
// matching, and a fact that belongs in one circle file silently becomes one
// file per reader: fourteen files and ninety-eight memories where seven
// belonged. Both sides have to come from the same roster.
func TestPlanStillMatchesACircleWhenTheSeededAdministratorIsInIt(t *testing.T) {
	projection := conversionProjection()
	projection.PersonAccessByPersonID[policy.SeededAdminPersonID] = policy.PersonAccess{
		PersonID:          policy.SeededAdminPersonID,
		Circles:           []string{"member"},
		SecurityLevelRank: 100,
		GrantedClasses:    []string{"internal", "executive"},
	}
	fact := legacyFact("fact-1", "The all-hands moved to Thursday.")
	fact.OwnerPersonID = ""
	fact.CircleIDs = []string{"member"}

	placements := memory.PlanConversion([]memory.LegacyFact{fact}, projection)

	want := []memory.Scope{memory.CircleScope("member")}
	if !sameScopes(placements[0].Destinations, want) {
		t.Fatalf("destinations = %v, want the circle's own file", placements[0].Destinations)
	}
}
