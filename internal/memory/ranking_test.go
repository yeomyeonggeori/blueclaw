package memory

import (
	"testing"
	"time"
)

func TestRankFactsFusesBothRankLists(t *testing.T) {
	referenceTime := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	both := RankedFact{Fact: Fact{FactID: "both", Kind: FactKindFact, ValidFrom: referenceTime}, VectorRank: 2, LexicalRank: 2}
	vectorOnly := RankedFact{Fact: Fact{FactID: "vector", Kind: FactKindFact, ValidFrom: referenceTime}, VectorRank: 1}
	ranked := RankFacts([]RankedFact{vectorOnly, both}, referenceTime)
	if ranked[0].Fact.FactID != "both" {
		t.Fatalf("expected the fact present in both lists to rank first, got %s", ranked[0].Fact.FactID)
	}
}

func TestRankFactsDecaysOldEpisodesAndKeepsBetterMatchesAheadOfReinforcedOnes(t *testing.T) {
	referenceTime := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	betterMatch := RankedFact{Fact: Fact{FactID: "fact", Kind: FactKindFact, ValidFrom: referenceTime}, VectorRank: 1, LexicalRank: 1}
	reinforced := RankedFact{Fact: Fact{FactID: "preference", Kind: FactKindPreference, ReinforcementCount: 9, ValidFrom: referenceTime}, VectorRank: 2, LexicalRank: 2}
	oldEpisode := RankedFact{Fact: Fact{FactID: "episode", Kind: FactKindEpisode, ValidFrom: referenceTime.Add(-180 * 24 * time.Hour)}, VectorRank: 1, LexicalRank: 1}
	ranked := RankFacts([]RankedFact{reinforced, oldEpisode, betterMatch}, referenceTime)
	if ranked[0].Fact.FactID != "fact" || ranked[1].Fact.FactID != "preference" || ranked[2].Fact.FactID != "episode" {
		t.Fatalf("expected the better match first, the reinforced preference second and the decayed episode last, got %s, %s, %s", ranked[0].Fact.FactID, ranked[1].Fact.FactID, ranked[2].Fact.FactID)
	}
}

func TestRankFactsBreaksTiesByReinforcementThenRecency(t *testing.T) {
	referenceTime := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	once := RankedFact{Fact: Fact{FactID: "once", Kind: FactKindPreference, ReinforcementCount: 1, ValidFrom: referenceTime}, LexicalRank: 2}
	often := RankedFact{Fact: Fact{FactID: "often", Kind: FactKindPreference, ReinforcementCount: 4, ValidFrom: referenceTime.Add(-time.Hour)}, LexicalRank: 2}
	ranked := RankFacts([]RankedFact{once, often}, referenceTime)
	if ranked[0].Fact.FactID != "often" {
		t.Fatalf("expected the reinforced preference to win a tie, got %s", ranked[0].Fact.FactID)
	}
}

func TestRankFactsBreaksTiesByNewerValidFrom(t *testing.T) {
	referenceTime := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	older := RankedFact{Fact: Fact{FactID: "older", Kind: FactKindFact, ValidFrom: referenceTime.Add(-time.Hour)}, LexicalRank: 3}
	newer := RankedFact{Fact: Fact{FactID: "newer", Kind: FactKindFact, ValidFrom: referenceTime}, LexicalRank: 3}
	ranked := RankFacts([]RankedFact{older, newer}, referenceTime)
	if ranked[0].Fact.FactID != "newer" {
		t.Fatalf("expected the newer fact first on a tie, got %s", ranked[0].Fact.FactID)
	}
}

func TestJobRetryDelayDoublesAndCaps(t *testing.T) {
	expectations := map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 3: 4 * time.Minute, 5: 16 * time.Minute, 12: 30 * time.Minute}
	for attempts, expectedDelay := range expectations {
		if delay := JobRetryDelay(attempts); delay != expectedDelay {
			t.Fatalf("expected attempt %d to wait %s, got %s", attempts, expectedDelay, delay)
		}
	}
}
