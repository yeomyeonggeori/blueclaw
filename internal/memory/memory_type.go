package memory

import "time"

type MemoryFact struct {
	FactID          string    `json:"factID"`
	ScopeType       string    `json:"scopeType"`
	ScopeID         string    `json:"scopeID,omitempty"`
	Content         string    `json:"content"`
	Score           float64   `json:"score"`
	SourceEpisodeID string    `json:"sourceEpisodeID"`
	SourceKind      string    `json:"sourceKind"`
	ValidAt         time.Time `json:"validAt"`
}
