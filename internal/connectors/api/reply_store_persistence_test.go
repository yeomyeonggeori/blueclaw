package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

func TestARestartedStoreStillHoldsTheReply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "agent-replies.json")

	before := NewPersistentReplyStore(path)
	before.Append("dm:api:person:1", "dm:api:person:1", connectors.OutboundReply{Message: "the answer"})

	after := NewPersistentReplyStore(path)
	replies := after.List("dm:api:person:1")
	if len(replies) != 1 || replies[0].Reply.Message != "the answer" {
		t.Fatalf("the reply did not survive the restart: %+v", replies)
	}
}

func TestAStoreWithNoPathStaysInMemory(t *testing.T) {
	store := NewReplyStore()
	store.Append("dm:api:person:2", "dm:api:person:2", connectors.OutboundReply{Message: "ephemeral"})
	if len(store.List("dm:api:person:2")) != 1 {
		t.Fatal("the in-memory store must still work for tests")
	}
}

func TestAnUnreadableFileStartsEmptyInsteadOfCrashing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-replies.json")
	if errorValue := os.WriteFile(path, []byte("not json"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	store := NewPersistentReplyStore(path)
	if len(store.List("anything")) != 0 {
		t.Fatal("garbage must read as empty")
	}
}
