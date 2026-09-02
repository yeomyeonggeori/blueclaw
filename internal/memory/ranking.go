package memory

import (
	"math"
	"sort"
	"time"
)

const (
	reciprocalRankOffset     = 60.0
	episodeDecayHalfLifeDays = 90.0
)

type ScoredFact struct {
	Fact        Fact    `json:"fact"`
	Score       float64 `json:"score"`
	VectorRank  int     `json:"vectorRank,omitempty"`
	LexicalRank int     `json:"lexicalRank,omitempty"`
}

func RankFacts(hits []RankedFact, referenceTime time.Time) []ScoredFact {
	scoredFacts := make([]ScoredFact, 0, len(hits))
	for _, hit := range hits {
		scoredFacts = append(scoredFacts, ScoredFact{
			Fact:        hit.Fact,
			Score:       adjustedScore(hit, referenceTime),
			VectorRank:  hit.VectorRank,
			LexicalRank: hit.LexicalRank,
		})
	}
	sort.SliceStable(scoredFacts, func(left int, right int) bool {
		if scoredFacts[left].Score != scoredFacts[right].Score {
			return scoredFacts[left].Score > scoredFacts[right].Score
		}
		if scoredFacts[left].Fact.ReinforcementCount != scoredFacts[right].Fact.ReinforcementCount {
			return scoredFacts[left].Fact.ReinforcementCount > scoredFacts[right].Fact.ReinforcementCount
		}
		return scoredFacts[left].Fact.ValidFrom.After(scoredFacts[right].Fact.ValidFrom)
	})
	return scoredFacts
}

func adjustedScore(hit RankedFact, referenceTime time.Time) float64 {
	score := reciprocalRankScore(hit.VectorRank) + reciprocalRankScore(hit.LexicalRank)
	if hit.Fact.Kind != FactKindEpisode {
		return score
	}
	ageDays := referenceTime.Sub(hit.Fact.ValidFrom).Hours() / 24
	if ageDays <= 0 {
		return score
	}
	return score * math.Exp(-ageDays/episodeDecayHalfLifeDays)
}

func reciprocalRankScore(rank int) float64 {
	if rank <= 0 {
		return 0
	}
	return 1 / (reciprocalRankOffset + float64(rank))
}
