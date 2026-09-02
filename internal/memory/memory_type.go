package memory

import "time"

const (
	ScopeTypeWorkspace = "workspace"
	ScopeTypeCircle    = "circle"
	ScopeTypePrivate   = "private"
)

type MemoryFact struct {
	FactID            string    `json:"factID"`
	ScopeType         string    `json:"scopeType"`
	Content           string    `json:"content"`
	Score             float64   `json:"score"`
	SourceEpisodeID   string    `json:"sourceEpisodeID"`
	SourceKind        string    `json:"sourceKind"`
	ValidAt           time.Time `json:"validAt"`
	SecurityLevelRank int       `json:"securityLevelRank"`
	RequiredClasses   []string  `json:"requiredClasses"`
}
