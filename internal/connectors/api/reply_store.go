package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

const conversationReplyCapacity = 50
const trackedConversationCapacity = 2000

type StoredReply struct {
	ConversationID string                   `json:"conversationID"`
	ReplyTargetID  string                   `json:"replyTargetID"`
	Reply          connectors.OutboundReply `json:"reply"`
}

type ReplyStore struct {
	mutex             sync.Mutex
	byConversation    map[string][]StoredReply
	conversationOrder []string
	path              string
}

func NewReplyStore() *ReplyStore {
	return &ReplyStore{byConversation: map[string][]StoredReply{}}
}

// A caller polls for replies over minutes, and the process serving the poll
// restarts under it: a deploy, a crash, a reprovision. A reply that was spoken
// has been spoken, so it comes back from disk rather than from luck.
func NewPersistentReplyStore(path string) *ReplyStore {
	replyStore := &ReplyStore{byConversation: map[string][]StoredReply{}, path: path}
	replyStore.load()
	return replyStore
}

type storedReplyDocument struct {
	ConversationOrder []string                 `json:"conversationOrder"`
	ByConversation    map[string][]StoredReply `json:"byConversation"`
}

func (replyStore *ReplyStore) load() {
	document, errorValue := os.ReadFile(replyStore.path)
	if errorValue != nil {
		return
	}
	var stored storedReplyDocument
	if json.Unmarshal(document, &stored) != nil {
		return
	}
	if stored.ByConversation != nil {
		replyStore.byConversation = stored.ByConversation
		replyStore.conversationOrder = stored.ConversationOrder
	}
}

func (replyStore *ReplyStore) persistLocked() {
	if replyStore.path == "" {
		return
	}
	document, errorValue := json.Marshal(storedReplyDocument{
		ConversationOrder: replyStore.conversationOrder,
		ByConversation:    replyStore.byConversation,
	})
	if errorValue != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(replyStore.path), 0o700) != nil {
		return
	}
	temporary := replyStore.path + ".writing"
	if os.WriteFile(temporary, document, 0o600) != nil {
		return
	}
	_ = os.Rename(temporary, replyStore.path)
}

func (replyStore *ReplyStore) Append(conversationID string, replyTargetID string, reply connectors.OutboundReply) {
	replyStore.mutex.Lock()
	defer replyStore.mutex.Unlock()
	existing, isTracked := replyStore.byConversation[conversationID]
	stored := append(existing, StoredReply{
		ConversationID: conversationID,
		ReplyTargetID:  replyTargetID,
		Reply:          reply,
	})
	if len(stored) > conversationReplyCapacity {
		stored = stored[len(stored)-conversationReplyCapacity:]
	}
	replyStore.byConversation[conversationID] = stored
	if !isTracked {
		replyStore.trackConversation(conversationID)
	}
	replyStore.persistLocked()
}

func (replyStore *ReplyStore) trackConversation(conversationID string) {
	replyStore.conversationOrder = append(replyStore.conversationOrder, conversationID)
	if len(replyStore.conversationOrder) <= trackedConversationCapacity {
		return
	}
	oldestConversationID := replyStore.conversationOrder[0]
	replyStore.conversationOrder = replyStore.conversationOrder[1:]
	delete(replyStore.byConversation, oldestConversationID)
}

func (replyStore *ReplyStore) List(conversationID string) []StoredReply {
	replyStore.mutex.Lock()
	defer replyStore.mutex.Unlock()
	return append([]StoredReply{}, replyStore.byConversation[conversationID]...)
}
