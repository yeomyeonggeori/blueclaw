package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

func TestRawEventRevisionsAgainstPostgres(t *testing.T) {
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
	fixturePrefix := "revision-test-" + time.Now().UTC().Format("20060102150405.000000000")
	conversationID := fixturePrefix + "-conversation"
	firstEvent := revisionTestEvent(conversationID, fixturePrefix+"-first", "first request")
	editedFirstEvent := firstEvent
	editedFirstEvent.EventID = fixturePrefix + "-first-edit"
	editedFirstEvent.Prompt = "edited first request"
	secondEvent := revisionTestEvent(conversationID, fixturePrefix+"-second", "revised request")
	thirdEvent := revisionTestEvent(conversationID, fixturePrefix+"-third", "unrelated request")
	fourthEvent := revisionTestEvent(conversationID, fixturePrefix+"-fourth", "delivery fixture")
	secondEvent.PreviousMessages = []connectors.PendingRequestMessage{{SourceReference: firstEvent.DedupeKey(), Prompt: firstEvent.Prompt}}
	editedFirstEvent.PreviousMessages = []connectors.PendingRequestMessage{{SourceReference: firstEvent.DedupeKey(), Prompt: firstEvent.Prompt}}
	secondEvent.PreviousMessages = []connectors.PendingRequestMessage{{SourceReference: editedFirstEvent.DedupeKey(), Prompt: editedFirstEvent.Prompt}}
	eventIDs := []string{firstEvent.DedupeKey(), editedFirstEvent.DedupeKey(), secondEvent.DedupeKey(), thirdEvent.DedupeKey(), fourthEvent.DedupeKey()}
	defer cleanRawEventRevisionFixtures(t, database, conversationID, eventIDs)

	if isDuplicate, _, errorValue := repository.TryEnqueueConnectorEvent(firstEvent); errorValue != nil || isDuplicate {
		t.Fatalf("enqueue first event: duplicate=%v error=%v", isDuplicate, errorValue)
	}
	firstOutboxID, errorValue := repository.EnqueueConnectorReply(firstEvent, revisionReplyTarget(firstEvent), connectors.OutboundReply{Message: "first reply", ReplyKind: "success"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if firstOutboxID == "" {
		t.Fatal("expected first reply outbox ID")
	}
	if isDuplicate, _, errorValue := repository.TryEnqueueConnectorEvent(editedFirstEvent); errorValue != nil || isDuplicate {
		t.Fatalf("enqueue edited first event: duplicate=%v error=%v", isDuplicate, errorValue)
	}
	editedFirstOutboxID, errorValue := repository.EnqueueConnectorReply(editedFirstEvent, revisionReplyTarget(editedFirstEvent), connectors.OutboundReply{Message: "edited first reply", ReplyKind: "success"})
	if errorValue != nil || editedFirstOutboxID == "" {
		t.Fatalf("enqueue edited first reply: outbox=%q error=%v", editedFirstOutboxID, errorValue)
	}
	assertOutboxReplyTarget(t, database, editedFirstOutboxID, editedFirstEvent.ReplyTargetID)
	if isDuplicate, _, errorValue := repository.TryEnqueueConnectorEvent(secondEvent); errorValue != nil || isDuplicate {
		t.Fatalf("enqueue second event: duplicate=%v error=%v", isDuplicate, errorValue)
	}
	secondOutboxID, errorValue := repository.EnqueueConnectorReply(secondEvent, revisionReplyTarget(secondEvent), connectors.OutboundReply{Message: "second reply", ReplyKind: "success"})
	if errorValue != nil || secondOutboxID == "" {
		t.Fatalf("enqueue second reply: outbox=%q error=%v", secondOutboxID, errorValue)
	}
	if isDuplicate, _, errorValue := repository.TryEnqueueConnectorEvent(thirdEvent); errorValue != nil || isDuplicate {
		t.Fatalf("enqueue third event: duplicate=%v error=%v", isDuplicate, errorValue)
	}
	if isDuplicate, _, errorValue := repository.TryEnqueueConnectorEvent(fourthEvent); errorValue != nil || isDuplicate {
		t.Fatalf("enqueue fourth event: duplicate=%v error=%v", isDuplicate, errorValue)
	}
	fourthOutboxID, errorValue := repository.EnqueueConnectorReply(fourthEvent, revisionReplyTarget(fourthEvent), connectors.OutboundReply{Message: "delivered fixture", ReplyKind: "success"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := repository.MarkConnectorReplySent(connectors.QueuedConnectorReply{OutboxID: fourthOutboxID}, "dispatch-fixture"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := repository.SuppressConnectorReply(connectors.QueuedConnectorReply{OutboxID: fourthOutboxID}, "late suppression"); errorValue != nil {
		t.Fatal(errorValue)
	}
	assertOutboxState(t, database, fourthOutboxID, "succeeded")

	assertRawEventRevisionState(t, database, firstEvent.DedupeKey(), "succeeded", connectors.SupersededRequestReason)
	assertOutboxState(t, database, firstOutboxID, "suppressed")
	if errorValue := repository.MarkConnectorEventFailed(connectors.QueuedConnectorEvent{Event: firstEvent}, errors.New("late failure"), time.Now().UTC()); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := repository.MarkConnectorEventSucceeded(firstEvent, connectors.ConnectorRuntimeResult{Handled: true, Reason: "late success"}); errorValue != nil {
		t.Fatal(errorValue)
	}
	assertRawEventRevisionState(t, database, firstEvent.DedupeKey(), "succeeded", connectors.SupersededRequestReason)
	assertRawEventRevisionState(t, database, editedFirstEvent.DedupeKey(), "succeeded", connectors.SupersededRequestReason)
	assertOutboxState(t, database, editedFirstOutboxID, "suppressed")
	if staleOutboxID, errorValue := repository.EnqueueConnectorReply(firstEvent, revisionReplyTarget(firstEvent), connectors.OutboundReply{Message: "stale reply", ReplyKind: "success"}); errorValue != nil || staleOutboxID != "" {
		t.Fatalf("expected stale reply to be skipped, outbox=%q error=%v", staleOutboxID, errorValue)
	}

	duplicateSecond := secondEvent
	duplicateSecond.PreviousMessages = []connectors.PendingRequestMessage{{SourceReference: thirdEvent.DedupeKey(), Prompt: thirdEvent.Prompt}}
	if isDuplicate, _, errorValue := repository.TryEnqueueConnectorEvent(duplicateSecond); errorValue != nil || !isDuplicate {
		t.Fatalf("enqueue duplicate second event: duplicate=%v error=%v", isDuplicate, errorValue)
	}
	assertRawEventRevisionState(t, database, thirdEvent.DedupeKey(), "pending", "")

	unansweredEvents, errorValue := repository.ListUnansweredConnectorEvents()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(unansweredEvents) != 2 || unansweredEvents[0].MessageID != secondEvent.MessageID || unansweredEvents[1].MessageID != thirdEvent.MessageID {
		t.Fatalf("expected latest unanswered lineage, got %+v", unansweredEvents)
	}
	assertOutboxState(t, database, secondOutboxID, "pending")
}

func revisionTestEvent(conversationID string, messageID string, prompt string) connectors.PlatformInboundEvent {
	return connectors.PlatformInboundEvent{
		Platform:       "revision-test",
		ConversationID: conversationID,
		MessageID:      messageID,
		SenderID:       "revision-test-sender",
		ReplyTargetID:  "revision-test-target-" + messageID,
		Prompt:         prompt,
		RawReceivedAt:  time.Now().UTC(),
	}
}

func revisionReplyTarget(event connectors.PlatformInboundEvent) connectors.ReplyTarget {
	return connectors.ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: event.ReplyTargetID, DedupeKey: event.DedupeKey()}
}

func assertRawEventRevisionState(t *testing.T, database Database, rawEventID string, expectedStatus string, expectedReason string) {
	t.Helper()
	var status string
	var reason sql.NullString
	if errorValue := database.SQL.QueryRowContext(context.Background(), `SELECT connector_status, connector_result_json->>'reason' FROM raw_event WHERE raw_event_id = $1`, rawEventID).Scan(&status, &reason); errorValue != nil {
		t.Fatal(errorValue)
	}
	if status != expectedStatus || reason.String != expectedReason {
		t.Fatalf("raw event %q: status=%q reason=%q", rawEventID, status, reason.String)
	}
}

func assertOutboxState(t *testing.T, database Database, outboxID string, expectedStatus string) {
	t.Helper()
	var status string
	if errorValue := database.SQL.QueryRowContext(context.Background(), `SELECT status FROM connector_outbox WHERE outbox_id = $1`, outboxID).Scan(&status); errorValue != nil {
		t.Fatal(errorValue)
	}
	if status != expectedStatus {
		t.Fatalf("outbox %q: status=%q, expected %q", outboxID, status, expectedStatus)
	}
}

func assertOutboxReplyTarget(t *testing.T, database Database, outboxID string, expectedReplyTargetID string) {
	t.Helper()
	var replyTargetID string
	if errorValue := database.SQL.QueryRowContext(context.Background(), `SELECT reply_target_id FROM connector_outbox WHERE outbox_id = $1`, outboxID).Scan(&replyTargetID); errorValue != nil {
		t.Fatal(errorValue)
	}
	if replyTargetID != expectedReplyTargetID {
		t.Fatalf("outbox %q: reply target=%q, expected %q", outboxID, replyTargetID, expectedReplyTargetID)
	}
}

func cleanRawEventRevisionFixtures(t *testing.T, database Database, conversationID string, rawEventIDs []string) {
	t.Helper()
	if _, errorValue := database.SQL.ExecContext(context.Background(), `DELETE FROM connector_outbox WHERE raw_event_id = ANY($1::text[])`, rawEventIDs); errorValue != nil {
		t.Logf("clean outbox fixtures: %v", errorValue)
	}
	if _, errorValue := database.SQL.ExecContext(context.Background(), `DELETE FROM raw_event WHERE raw_event_id = ANY($1::text[])`, rawEventIDs); errorValue != nil {
		t.Logf("clean raw event fixtures: %v", errorValue)
	}
	if _, errorValue := database.SQL.ExecContext(context.Background(), `DELETE FROM conversation WHERE conversation_id = $1`, "revision-test:"+conversationID); errorValue != nil {
		t.Logf("clean conversation fixture: %v", errorValue)
	}
}
