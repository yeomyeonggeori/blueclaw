package memory

import "time"

const (
	ScopeTypeUser         = "user"
	ScopeTypeWorkspace    = "workspace"
	ScopeTypeConversation = "conversation"
	ScopeTypeCircle       = "circle"
	ScopeTypePrivate      = "private"
)

const (
	MemorySourceKindFact    = "fact"
	MemorySourceKindNode    = "node"
	MemorySourceKindEpisode = "episode"
	MemorySourceKindPinned  = "pinned"
)

type MemoryNamespace struct {
	NamespaceID         string   `json:"namespaceID"`
	ScopeType           string   `json:"scopeType"`
	ScopePersonID       string   `json:"scopePersonID,omitempty"`
	ScopeConversationID string   `json:"scopeConversationID,omitempty"`
	ScopeCircleID       string   `json:"scopeCircleID,omitempty"`
	SecurityLevelRank   int      `json:"securityLevelRank"`
	RequiredClasses     []string `json:"requiredClasses"`
}

type MemoryEpisode struct {
	EpisodeID       string            `json:"episodeID"`
	Platform        string            `json:"platform"`
	MessageID       string            `json:"messageID"`
	ConversationID  string            `json:"conversationID"`
	SenderPersonID  string            `json:"senderPersonID"`
	Prompt          string            `json:"prompt"`
	OccurredAt      time.Time         `json:"occurredAt"`
	Namespaces      []MemoryNamespace `json:"namespaces"`
	Source          string            `json:"source"`
	SourceReference string            `json:"sourceReference"`
}

type MemoryIngestionResult struct {
	EpisodeID      string `json:"episodeID"`
	NamespaceCount int    `json:"namespaceCount"`
}

type MemoryEpisodeDeleteRequest struct {
	EpisodeID    string   `json:"episodeID"`
	NamespaceIDs []string `json:"namespaceIDs"`
}

type MemoryEpisodeDeleteResult struct {
	EpisodeID      string `json:"episodeID"`
	Deleted        bool   `json:"deleted"`
	NamespaceCount int    `json:"namespaceCount"`
}

type MemoryFactUpdateRequest struct {
	NamespaceID string     `json:"namespaceID"`
	FactID      string     `json:"factID"`
	Content     string     `json:"content"`
	ValidAt     *time.Time `json:"validAt,omitempty"`
	InvalidAt   *time.Time `json:"invalidAt,omitempty"`
	ExpiredAt   *time.Time `json:"expiredAt,omitempty"`
}

type MemoryFactDeleteRequest struct {
	NamespaceID string `json:"namespaceID"`
	FactID      string `json:"factID"`
}

type MemoryFactMutationResult struct {
	FactID      string `json:"factID"`
	NamespaceID string `json:"namespaceID"`
	Deleted     bool   `json:"deleted"`
}

type MemoryFact struct {
	FactID            string     `json:"factID"`
	ScopeType         string     `json:"scopeType"`
	NamespaceID       string     `json:"namespaceID"`
	Content           string     `json:"content"`
	Score             float64    `json:"score"`
	SourceEpisodeID   string     `json:"sourceEpisodeID"`
	SourceEpisodeIDs  []string   `json:"sourceEpisodeIDs,omitempty"`
	SourceKind        string     `json:"sourceKind"`
	ValidAt           time.Time  `json:"validAt"`
	RecordedAt        time.Time  `json:"recordedAt"`
	InvalidAt         *time.Time `json:"invalidAt,omitempty"`
	ExpiredAt         *time.Time `json:"expiredAt,omitempty"`
	SecurityLevelRank int        `json:"securityLevelRank"`
	RequiredClasses   []string   `json:"requiredClasses"`
}

type MemoryRetrieval struct {
	Query    string                   `json:"query"`
	Complete bool                     `json:"complete"`
	Limit    int                      `json:"limit"`
	Failures []MemoryRetrievalFailure `json:"failures"`
}

type MemoryRetrievalFailure struct {
	NamespaceID string `json:"namespaceID"`
	Message     string `json:"message"`
}

type MemoryHealth struct {
	Configured         bool   `json:"configured"`
	Reachable          bool   `json:"reachable"`
	LastSearchError    string `json:"lastSearchError,omitempty"`
	LastIngestionError string `json:"lastIngestionError,omitempty"`
	Error              string `json:"error,omitempty"`
}

type MemoryGraphNamespace struct {
	NamespaceID         string    `json:"namespaceID"`
	ScopeType           string    `json:"scopeType"`
	ScopePersonID       string    `json:"scopePersonID,omitempty"`
	ScopeConversationID string    `json:"scopeConversationID,omitempty"`
	ScopeCircleID       string    `json:"scopeCircleID,omitempty"`
	SecurityLevelRank   int       `json:"securityLevelRank"`
	RequiredClasses     []string  `json:"requiredClasses"`
	EpisodeCount        int       `json:"episodeCount"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type MemoryGraphEpisode struct {
	EpisodeID       string    `json:"episodeID"`
	Platform        string    `json:"platform"`
	MessageID       string    `json:"messageID"`
	ConversationID  string    `json:"conversationID"`
	SenderPersonID  string    `json:"senderPersonID"`
	Prompt          string    `json:"prompt,omitempty"`
	NamespaceIDs    []string  `json:"namespaceIDs"`
	IngestionStatus string    `json:"ingestionStatus"`
	IngestionError  string    `json:"ingestionError,omitempty"`
	OccurredAt      time.Time `json:"occurredAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type MemoryGraphNode struct {
	NodeID    string `json:"nodeID"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	ScopeType string `json:"scopeType,omitempty"`
	Status    string `json:"status,omitempty"`
}

type MemoryGraphEdge struct {
	SourceID string `json:"sourceID"`
	TargetID string `json:"targetID"`
	Label    string `json:"label"`
	Weight   int    `json:"weight"`
}

type MemoryGraph struct {
	Health     MemoryHealth           `json:"health"`
	Namespaces []MemoryGraphNamespace `json:"namespaces"`
	Episodes   []MemoryGraphEpisode   `json:"episodes"`
	Facts      []MemoryFact           `json:"facts"`
	Nodes      []MemoryGraphNode      `json:"nodes"`
	Edges      []MemoryGraphEdge      `json:"edges"`
	Retrieval  MemoryRetrieval        `json:"retrieval"`
}

type MemoryIdentityMapping struct {
	OldPersonID string `json:"oldPersonID"`
	NewPersonID string `json:"newPersonID"`
}

type MemoryIdentityMigrationResult struct {
	OldPersonID       string `json:"oldPersonID"`
	NewPersonID       string `json:"newPersonID"`
	NamespacesUpdated int64  `json:"namespacesUpdated"`
	EpisodesUpdated   int64  `json:"episodesUpdated"`
	MarkdownRenamed   bool   `json:"markdownRenamed"`
	MarkdownConflict  bool   `json:"markdownConflict"`
}
