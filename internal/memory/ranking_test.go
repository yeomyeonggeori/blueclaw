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

func TestRankFactsDecaysOldEpisodesAndRewardsReinforcedPreferences(t *testing.T) {
	referenceTime := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	freshFact := RankedFact{Fact: Fact{FactID: "fact", Kind: FactKindFact, ValidFrom: referenceTime}, LexicalRank: 1}
	oldEpisode := RankedFact{Fact: Fact{FactID: "episode", Kind: FactKindEpisode, ValidFrom: referenceTime.Add(-180 * 24 * time.Hour)}, LexicalRank: 1}
	preference := RankedFact{Fact: Fact{FactID: "preference", Kind: FactKindPreference, ReinforcementCount: 9, ValidFrom: referenceTime}, LexicalRank: 1}
	ranked := RankFacts([]RankedFact{oldEpisode, freshFact, preference}, referenceTime)
	if ranked[0].Fact.FactID != "preference" || ranked[1].Fact.FactID != "fact" || ranked[2].Fact.FactID != "episode" {
		t.Fatalf("expected preference, fact, episode order, got %s, %s, %s", ranked[0].Fact.FactID, ranked[1].Fact.FactID, ranked[2].Fact.FactID)
	}
	expectedPreferenceScore := reciprocalRankScore(1) * (1 + preferenceReinforcementBonus*preferenceReinforcementCap)
	if ranked[0].Score != expectedPreferenceScore {
		t.Fatalf("expected the reinforcement bonus to cap at %d, got score %f", preferenceReinforcementCap, ranked[0].Score)
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
