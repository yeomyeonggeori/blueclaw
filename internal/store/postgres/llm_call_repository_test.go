package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func openLLMCallTestDatabase(t *testing.T) Database {
	t.Helper()
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("set BLUECLAW_TEST_POSTGRES_URL to run the disposable PostgreSQL regression")
	}
	database, errorValue := OpenDatabase(context.Background(), connectionString, 0)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { database.Close() })
	migrationRunner := MigrationRunner{MigrationDirectoryPath: filepath.Join("..", "..", "..", "migrations")}
	if errorValue := migrationRunner.ApplyMigrations(context.Background(), database); errorValue != nil {
		t.Fatal(errorValue)
	}
	return database
}

func insertLLMCallTestTaskRun(t *testing.T, database Database, taskRunID string) {
	t.Helper()
	_, errorValue := database.SQL.ExecContext(context.Background(), `
INSERT INTO task_run (task_run_id, current_agent_profile_name, status, prompt, created_at, updated_at)
VALUES ($1, 'assistant', 'completed', 'llm call fixture', now(), now())`, taskRunID)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() {
		database.SQL.ExecContext(context.Background(), `DELETE FROM task_run WHERE task_run_id = $1`, taskRunID)
	})
}

func llmCallTestRecord(lastMessage string) agentcontract.LLMCallRecord {
	systemPrompt := strings.Repeat("You are the company's agent. ", 40)
	return agentcontract.LLMCallRecord{
		Kind: "chat",
		Exchange: &model.WireExchange{
			Endpoint: "https://router.example.com/v1/chat/completions",
			Request:  `{"model":"z-ai/glm-5.3","messages":[{"role":"system","content":"` + systemPrompt + `"},{"role":"user","content":"` + lastMessage + `"}],"seed":1234567}`,
			Response: `{"provider":"Example","choices":[{"message":{"content":"done"}}]}`,
		},
	}
}

func llmCallTestEvent(taskRunID string, llmCallID string) task.TaskEvent {
	return task.TaskEvent{TaskEventID: llmCallID, TaskRunID: taskRunID, Name: agentcontract.TaskEventLLMCall, Body: `{"kind":"chat","costUSD":0.01}`, CreatedAt: time.Now()}
}

func countExchangeParts(t *testing.T, database Database) int {
	t.Helper()
	var count int
	if errorValue := database.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM ledger_part`).Scan(&count); errorValue != nil {
		t.Fatal(errorValue)
	}
	return count
}

func TestLLMCallRepositoryAgainstPostgres(t *testing.T) {
	database := openLLMCallTestDatabase(t)
	repository := NewLLMCallRepository(database)
	fixturePrefix := "llm-call-test-" + time.Now().UTC().Format("20060102150405.000000000")
	taskRunID := fixturePrefix + "-run"
	insertLLMCallTestTaskRun(t, database, taskRunID)

	firstRecord := llmCallTestRecord("first request")
	firstRecord.Input = json.RawMessage(`{"messages":[{"id":"message-1"}]}`)
	if errorValue := repository.InsertLLMCall(llmCallTestEvent(taskRunID, fixturePrefix+"-first"), firstRecord); errorValue != nil {
		t.Fatalf("insert the first call: %v", errorValue)
	}
	partsAfterFirstCall := countExchangeParts(t, database)
	if errorValue := repository.InsertLLMCall(llmCallTestEvent(taskRunID, fixturePrefix+"-second"), llmCallTestRecord("second request")); errorValue != nil {
		t.Fatalf("insert the second call: %v", errorValue)
	}
	if addedParts := countExchangeParts(t, database) - partsAfterFirstCall; addedParts != 1 {
		t.Fatalf("expected the second call to add only its own request, added %d parts", addedParts)
	}

	stored, isFound, errorValue := repository.FindLLMCallExchange(fixturePrefix + "-first")
	if errorValue != nil || !isFound {
		t.Fatalf("expected the first exchange back: found=%v error=%v", isFound, errorValue)
	}
	if stored.Request != firstRecord.Exchange.Request || stored.Response != firstRecord.Exchange.Response || stored.Input != string(firstRecord.Input) {
		t.Fatalf("expected the very bytes that were sent and answered, got %+v", stored)
	}

	taskEvents, errorValue := NewTaskEventRepository(database).ListTaskEvent(taskRunID)
	if errorValue != nil || len(taskEvents) != 2 || taskEvents[0].Name != agentcontract.TaskEventLLMCall || taskEvents[0].Body != `{"kind":"chat","costUSD":0.01}` {
		t.Fatalf("expected both calls among the task's events with their record bodies, got %+v error=%v", taskEvents, errorValue)
	}
}

func TestTasklessLLMCallsAgeOutWithTheirParts(t *testing.T) {
	database := openLLMCallTestDatabase(t)
	repository := NewLLMCallRepository(database)
	fixturePrefix := "llm-call-age-" + time.Now().UTC().Format("20060102150405.000000000")
	taskRunID := fixturePrefix + "-run"
	insertLLMCallTestTaskRun(t, database, taskRunID)
	repository.InsertLLMCall(llmCallTestEvent(taskRunID, fixturePrefix+"-kept"), llmCallTestRecord("kept"))
	tasklessEvent := llmCallTestEvent("", fixturePrefix+"-taskless")
	tasklessEvent.CreatedAt = time.Now().Add(-40 * 24 * time.Hour)
	if errorValue := repository.InsertTasklessLLMCall(tasklessEvent, []string{fixturePrefix + "-message"}, llmCallTestRecord("aged out")); errorValue != nil {
		t.Fatalf("insert the taskless call: %v", errorValue)
	}

	prunedCount, errorValue := repository.PruneTasklessLLMCallsBefore(time.Now().Add(-30 * 24 * time.Hour))
	if errorValue != nil || prunedCount != 1 {
		t.Fatalf("expected the old taskless call pruned, pruned=%d error=%v", prunedCount, errorValue)
	}
	if _, errorValue := repository.DeleteUnreferencedLedgerParts(); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, isFound, errorValue := repository.FindLLMCallExchange(fixturePrefix + "-kept"); errorValue != nil || !isFound {
		t.Fatalf("expected the task's call to keep the parts it shares, found=%v error=%v", isFound, errorValue)
	}
	var agedPartCount int
	database.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM ledger_part WHERE body LIKE '%aged out%'`).Scan(&agedPartCount)
	if agedPartCount != 0 {
		t.Fatalf("expected the pruned call's own parts gone, %d remain", agedPartCount)
	}
}

func TestInboundDiagnosticsCarryTheUnclaimedDecision(t *testing.T) {
	database := openLLMCallTestDatabase(t)
	fixturePrefix := "llm-call-inbound-" + time.Now().UTC().Format("20060102150405.000000000")
	event := revisionTestEvent(fixturePrefix+"-conversation", fixturePrefix+"-message", "다음 주 출시 확정됐어요!")
	event.Context.Sender.Name = "박예시"
	defer cleanRawEventRevisionFixtures(t, database, event.ConversationID, []string{event.DedupeKey()})
	if _, _, errorValue := NewRawEventRepository(database).TryEnqueueConnectorEvent(event); errorValue != nil {
		t.Fatal(errorValue)
	}
	decisionEvent := task.TaskEvent{TaskEventID: fixturePrefix + "-decision", Name: agentcontract.TaskEventLLMCall, Body: `{"kind":"decision","decisionDraws":{"m1.reaction":0.81}}`, CreatedAt: time.Now()}
	if errorValue := NewLLMCallRepository(database).InsertTasklessLLMCall(decisionEvent, []string{"another-message", event.MessageID}, llmCallTestRecord("decide")); errorValue != nil {
		t.Fatal(errorValue)
	}

	diagnostics, errorValue := NewRawEventRepository(database).ListConnectorEventDiagnostics(context.Background(), connectors.EventDiagnosticFilter{MessageID: event.MessageID})
	if errorValue != nil || len(diagnostics) != 1 {
		t.Fatalf("expected the inbound message, got %+v error=%v", diagnostics, errorValue)
	}
	diagnostic := diagnostics[0]
	if diagnostic.SenderName != "박예시" || diagnostic.PromptPreview != "다음 주 출시 확정됐어요!" || diagnostic.DecisionCallID != decisionEvent.TaskEventID || !strings.Contains(string(diagnostic.Decision), "0.81") {
		t.Fatalf("expected the sender, the words and the decision beside the message, got %+v", diagnostic)
	}
}

func TestLLMCallMigrationMovesRecordedCallsOutOfTheEventLedger(t *testing.T) {
	database := openLLMCallTestDatabase(t)
	fixturePrefix := "llm-call-migration-" + time.Now().UTC().Format("20060102150405.000000000")
	taskRunID := fixturePrefix + "-run"
	insertLLMCallTestTaskRun(t, database, taskRunID)
	if errorValue := NewTaskEventRepository(database).InsertTaskEvent(llmCallTestEvent(taskRunID, fixturePrefix+"-recorded")); errorValue != nil {
		t.Fatal(errorValue)
	}
	migration, errorValue := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "036_llm_call.sql"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if errorValue := database.Exec(context.Background(), string(migration)); errorValue != nil {
		t.Fatalf("expected the migration to apply again: %v", errorValue)
	}

	var eventCount, callCount int
	database.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM task_event WHERE task_run_id = $1`, taskRunID).Scan(&eventCount)
	database.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM llm_call WHERE task_run_id = $1`, taskRunID).Scan(&callCount)
	if eventCount != 0 || callCount != 1 {
		t.Fatalf("expected the recorded call moved into llm_call, events=%d calls=%d", eventCount, callCount)
	}
}

func TestAPartedTaskEventKeepsItsPartsThroughCollection(t *testing.T) {
	database := openLLMCallTestDatabase(t)
	taskEventRepository := NewTaskEventRepository(database)
	fixturePrefix := "parted-event-" + time.Now().UTC().Format("20060102150405.000000000")
	taskRunID := fixturePrefix + "-run"
	insertLLMCallTestTaskRun(t, database, taskRunID)
	turnInput := `{"Prompt":"회의록 정리해줘","HostInstruction":"` + strings.Repeat("Answer in Korean. ", 60) + `"}`
	taskEvent := task.TaskEvent{TaskEventID: fixturePrefix + "-input", TaskRunID: taskRunID, Name: agentcontract.TaskEventTaskTurnInput, CreatedAt: time.Now()}

	if errorValue := taskEventRepository.InsertPartedTaskEvent(taskEvent, json.RawMessage(turnInput)); errorValue != nil {
		t.Fatalf("insert the turn input: %v", errorValue)
	}
	if _, errorValue := NewLLMCallRepository(database).DeleteUnreferencedLedgerParts(); errorValue != nil {
		t.Fatal(errorValue)
	}

	document, isFound, errorValue := taskEventRepository.FindPartedTaskEventDocument(taskEvent.TaskEventID)
	if errorValue != nil || !isFound || document != turnInput {
		t.Fatalf("expected the turn input back unchanged after collection, found=%v error=%v document=%s", isFound, errorValue, document)
	}
}
