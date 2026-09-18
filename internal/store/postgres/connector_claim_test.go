package postgres

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

func TestAClaimTakesOneConversationAndLeavesTheOtherClaimable(t *testing.T) {
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("set BLUECLAW_TEST_POSTGRES_URL to run the disposable PostgreSQL regression")
	}

	database, errorValue := OpenDatabase(context.Background(), connectionString)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer database.Close()
	migrationRunner := MigrationRunner{MigrationDirectoryPath: filepath.Join("..", "..", "..", "migrations")}
	if errorValue := migrationRunner.ApplyMigrations(context.Background(), database); errorValue != nil {
		t.Fatal(errorValue)
	}

	repository := NewRawEventRepository(database)
	fixturePrefix := "claim-test-" + time.Now().UTC().Format("20060102150405.000000000")
	busyConversation := fixturePrefix + "-conversation-1"
	waitingConversation := fixturePrefix + "-conversation-2"
	events := []connectors.PlatformInboundEvent{
		claimTestEvent(busyConversation, fixturePrefix+"-first"),
		claimTestEvent(waitingConversation, fixturePrefix+"-second"),
		claimTestEvent(busyConversation, fixturePrefix+"-third"),
	}
	rawEventIDs := []string{}
	for _, event := range events {
		rawEventIDs = append(rawEventIDs, event.DedupeKey())
	}
	defer cleanClaimFixtures(t, database, rawEventIDs, []string{busyConversation, waitingConversation})
	for _, event := range events {
		if isDuplicate, _, errorValue := repository.TryEnqueueConnectorEvent(event); errorValue != nil || isDuplicate {
			t.Fatalf("enqueue %s: duplicate=%v error=%v", event.MessageID, isDuplicate, errorValue)
		}
	}

	claimedConversations := map[string]int{}
	for claimCount := 0; claimCount < 10 && len(claimedConversations) < 2; claimCount++ {
		queuedEvents, errorValue := repository.ClaimPendingConnectorEvents(4, time.Minute)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if len(queuedEvents) == 0 {
			break
		}
		conversationsInClaim := map[string]int{}
		for _, queuedEvent := range queuedEvents {
			conversationsInClaim[queuedEvent.Event.ConversationID]++
		}
		if len(conversationsInClaim) != 1 {
			t.Fatalf("expected one claim to hold one conversation, got %v", conversationsInClaim)
		}
		for conversationID, messageCount := range conversationsInClaim {
			claimedConversations[conversationID] = messageCount
		}
	}

	if claimedConversations[busyConversation] != 2 {
		t.Fatalf("expected the busy conversation's two messages in one claim, got %v", claimedConversations)
	}
	if claimedConversations[waitingConversation] != 1 {
		t.Fatalf("expected the other conversation to stay claimable by a second worker, got %v", claimedConversations)
	}
}

func claimTestEvent(conversationID string, messageID string) connectors.PlatformInboundEvent {
	return connectors.PlatformInboundEvent{
		Platform:       "claim-test",
		ConversationID: conversationID,
		MessageID:      messageID,
		SenderID:       "claim-test-sender",
		ReplyTargetID:  "claim-test-target-" + messageID,
		Prompt:         "이거 정리해줘",
		RawReceivedAt:  time.Now().UTC(),
	}
}

func cleanClaimFixtures(t *testing.T, database Database, rawEventIDs []string, conversationIDs []string) {
	t.Helper()
	if _, errorValue := database.SQL.ExecContext(context.Background(), `DELETE FROM connector_outbox WHERE raw_event_id = ANY($1::text[])`, rawEventIDs); errorValue != nil {
		t.Logf("clean outbox fixtures: %v", errorValue)
	}
	if _, errorValue := database.SQL.ExecContext(context.Background(), `DELETE FROM raw_event WHERE raw_event_id = ANY($1::text[])`, rawEventIDs); errorValue != nil {
		t.Logf("clean raw event fixtures: %v", errorValue)
	}
	for _, conversationID := range conversationIDs {
		if _, errorValue := database.SQL.ExecContext(context.Background(), `DELETE FROM conversation WHERE conversation_id = $1`, "claim-test:"+conversationID); errorValue != nil {
			t.Logf("clean conversation fixture: %v", errorValue)
		}
	}
}
