package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluememo"
)

// LegacyFact is one memory as the retired store held it: a sentence scoped by
// an owner and by circles, narrowed by a clearance rank and a set of classes.
type LegacyFact struct {
	FactID            string
	OwnerPersonID     string
	Content           string
	IsStatic          bool
	CircleIDs         []string
	SecurityLevelRank int
	RequiredClasses   []string
	OccurredAt        time.Time
	ValidUntil        time.Time
	CreatedAt         time.Time
	Embedding         []float32
	EmbeddingModel    string
}

// Placement is where one fact lands and the audience that decided it. The
// destinations are files whose readers are exactly that audience, so a
// conversion neither widens nor narrows who knows a thing.
type Placement struct {
	FactID       string
	OldScope     string
	Audience     []string
	Destinations []Scope
	Boundary     string
}

// ErrCarriedEmbeddingWidth reports a carried vector that is not the width the
// store should hold. A fresh file takes the first width it is given as the one
// for that model, so one wrong vector arriving first would make every correct
// one after it the mismatch.
var ErrCarriedEmbeddingWidth = errors.New("a carried embedding is not the width the store expects")

// ConversionReport counts what a conversion wrote.
type ConversionReport struct {
	Facts       int `json:"facts"`
	Memories    int `json:"memories"`
	AlreadyHeld int `json:"alreadyHeld"`
}

// PlanConversion works out who may read each fact today, by the rule the
// retired store read it with, and chooses the files in the new model whose
// readers are exactly those people.
//
// Nothing is written. The plan is the thing to read before a conversion runs.
func PlanConversion(facts []LegacyFact, projection policy.PolicyProjection) []Placement {
	readableCircles := readableCirclesByPerson(projection)
	membersByCircle := membersByCircle(readableCircles)
	placements := make([]Placement, 0, len(facts))
	for _, fact := range facts {
		audience := factAudience(fact, projection, readableCircles)
		placements = append(placements, placeFact(fact, audience, membersByCircle))
	}
	return placements
}

// Convert writes the planned memories, carrying each sentence and its
// embedding as they are. It is idempotent, so a conversion interrupted
// halfway finishes by running again.
func (stores *Stores) Convert(ctx context.Context, facts []LegacyFact, projection policy.PolicyProjection, expectedEmbeddingWidth int) (ConversionReport, error) {
	if errorValue := refuseUnexpectedEmbeddingWidth(facts, expectedEmbeddingWidth); errorValue != nil {
		return ConversionReport{}, errorValue
	}
	factByID := map[string]LegacyFact{}
	for _, fact := range facts {
		factByID[fact.FactID] = fact
	}
	report := ConversionReport{}
	for _, placement := range PlanConversion(facts, projection) {
		fact := factByID[placement.FactID]
		for _, destination := range placement.Destinations {
			store, errorValue := stores.Store(ctx, destination)
			if errorValue != nil {
				return report, fmt.Errorf("open %s for %s: %w", destination.ID, fact.FactID, errorValue)
			}
			adopted := adoptedFromLegacy(fact, destination)
			adoptReport, errorValue := store.Adopt(ctx, []bluememo.AdoptedMemory{adopted})
			if errorValue != nil {
				return report, fmt.Errorf("adopt %s into %s: %w", fact.FactID, destination.ID, errorValue)
			}
			report.Memories += adoptReport.Adopted
			report.AlreadyHeld += adoptReport.AlreadyHeld
		}
		report.Facts++
	}
	return report, nil
}

// refuseUnexpectedEmbeddingWidth checks every vector before the first write,
// so a conversion that cannot carry the index does not carry half of it.
func refuseUnexpectedEmbeddingWidth(facts []LegacyFact, expectedEmbeddingWidth int) error {
	if expectedEmbeddingWidth <= 0 {
		return nil
	}
	for _, fact := range facts {
		if len(fact.Embedding) == 0 || len(fact.Embedding) == expectedEmbeddingWidth {
			continue
		}
		return fmt.Errorf("%w: %s carries %d from %s, and the store holds %d",
			ErrCarriedEmbeddingWidth, fact.FactID, len(fact.Embedding), fact.EmbeddingModel, expectedEmbeddingWidth)
	}
	return nil
}

// adoptedFromLegacy derives the memory identifier from the fact and its
// destination, so one fact carried to several files keeps a stable identity in
// each of them and a second run recognises what it already wrote.
func adoptedFromLegacy(fact LegacyFact, destination Scope) bluememo.AdoptedMemory {
	memoryID := fact.FactID
	if destination.Kind == ScopePerson {
		memoryID = fact.FactID + "." + destination.ID
	}
	return bluememo.AdoptedMemory{
		Memory: bluememo.Memory{
			MemoryID:   memoryID,
			Content:    fact.Content,
			IsStatic:   fact.IsStatic,
			OccurredAt: fact.OccurredAt,
			ValidUntil: fact.ValidUntil,
			OriginID:   fact.FactID,
			CreatedAt:  fact.CreatedAt,
		},
		Embedding:      fact.Embedding,
		EmbeddingModel: fact.EmbeddingModel,
	}
}

// factAudience reproduces the retired reader: an owner reads their own fact
// whatever its rank, and everyone else needs a shared circle, the rank and
// every required class.
func factAudience(fact LegacyFact, projection policy.PolicyProjection, readableCircles map[string]map[string]bool) []string {
	audience := map[string]bool{}
	if fact.OwnerPersonID != "" {
		audience[fact.OwnerPersonID] = true
	}
	for personID, personAccess := range projection.PersonAccessByPersonID {
		if !sharesACircle(fact.CircleIDs, readableCircles[personID]) {
			continue
		}
		if personAccess.SecurityLevelRank < fact.SecurityLevelRank {
			continue
		}
		if !holdsEveryClass(personAccess.GrantedClasses, fact.RequiredClasses) {
			continue
		}
		audience[personID] = true
	}
	return sortedKeys(audience)
}

// placeFact chooses the narrowest boundary the new model already has. A single
// reader is their own file. A circle whose members are exactly the audience is
// that circle's file. Anything else is each reader's own file, which is how the
// new model says that these particular people know a thing.
func placeFact(fact LegacyFact, audience []string, membersByCircle map[string][]string) Placement {
	placement := Placement{
		FactID:   fact.FactID,
		OldScope: legacyScopeName(fact),
		Audience: audience,
	}
	if len(audience) == 1 {
		placement.Destinations = []Scope{PersonScope(audience[0])}
		placement.Boundary = "the reader's own file"
		return placement
	}
	for _, circleID := range sortedCircleIDs(membersByCircle) {
		if samepeople(membersByCircle[circleID], audience) {
			placement.Destinations = []Scope{CircleScope(circleID)}
			placement.Boundary = "circle " + circleID
			return placement
		}
	}
	for _, personID := range audience {
		placement.Destinations = append(placement.Destinations, PersonScope(personID))
	}
	placement.Boundary = fmt.Sprintf("%d readers' own files", len(audience))
	return placement
}

func legacyScopeName(fact LegacyFact) string {
	if len(fact.CircleIDs) == 0 {
		return "person:" + fact.OwnerPersonID
	}
	circleIDs := append([]string{}, fact.CircleIDs...)
	sort.Strings(circleIDs)
	return "circle:" + strings.Join(circleIDs, ",")
}

func readableCirclesByPerson(projection policy.PolicyProjection) map[string]map[string]bool {
	readable := map[string]map[string]bool{}
	for personID, personAccess := range projection.PersonAccessByPersonID {
		reached := map[string]bool{}
		pending := append([]string{}, personAccess.Circles...)
		for len(pending) > 0 {
			circleID := pending[0]
			pending = pending[1:]
			if reached[circleID] {
				continue
			}
			reached[circleID] = true
			pending = append(pending, projection.ContainedCirclesByID[circleID]...)
		}
		readable[personID] = reached
	}
	return readable
}

// membersByCircle lists who reads each circle, which is every person the
// circle reaches rather than only those who name it.
func membersByCircle(readableCircles map[string]map[string]bool) map[string][]string {
	members := map[string]map[string]bool{}
	for personID, circles := range readableCircles {
		for circleID := range circles {
			if members[circleID] == nil {
				members[circleID] = map[string]bool{}
			}
			members[circleID][personID] = true
		}
	}
	byCircle := map[string][]string{}
	for circleID, people := range members {
		byCircle[circleID] = sortedKeys(people)
	}
	return byCircle
}

func sharesACircle(factCircleIDs []string, readable map[string]bool) bool {
	for _, circleID := range factCircleIDs {
		if readable[circleID] {
			return true
		}
	}
	return false
}

func holdsEveryClass(granted []string, required []string) bool {
	held := map[string]bool{}
	for _, class := range granted {
		held[class] = true
	}
	for _, class := range required {
		if !held[class] {
			return false
		}
	}
	return true
}

func samepeople(left []string, right []string) bool {
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

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedCircleIDs(membersByCircle map[string][]string) []string {
	circleIDs := make([]string, 0, len(membersByCircle))
	for circleID := range membersByCircle {
		circleIDs = append(circleIDs, circleID)
	}
	sort.Strings(circleIDs)
	return circleIDs
}
