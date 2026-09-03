package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

func TestMigrationsApplyList(t *testing.T) {
	migrationRunner := postgres.MigrationRunner{MigrationDirectoryPath: "../../migrations"}
	migrationPaths, errorValue := migrationRunner.ListMigrationPath()
	if errorValue != nil {
		t.Fatalf("expected migrations to load: %v", errorValue)
	}
	if len(migrationPaths) != 31 {
		t.Fatalf("expected 31 migration files, got %d", len(migrationPaths))
	}
}

func TestMinimalConversationContractMigrationStoresReplayFields(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "009_minimal_conversation_contract.sql"))
	if errorValue != nil {
		t.Fatalf("expected minimal conversation migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"reply_target_id text",
		"visible_context_ciphertext bytea",
		"visible_context_sha256 bytea",
		"has_more_before boolean NOT NULL DEFAULT false",
		"history_cursor text",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}

func TestGraphitiEpisodePromptMigrationStoresInspectableSource(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "028_graphiti_episode_prompt.sql"))
	if errorValue != nil {
		t.Fatalf("expected graphiti episode prompt migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"ALTER TABLE graphiti_episode",
		"ADD COLUMN IF NOT EXISTS prompt text NOT NULL DEFAULT ''",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}

func TestGraphitiEpisodePromptBackfillMigrationRestoresRawEventContent(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "029_backfill_graphiti_episode_prompt.sql"))
	if errorValue != nil {
		t.Fatalf("expected graphiti episode prompt backfill migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"UPDATE graphiti_episode episode",
		"SET prompt = convert_from(raw_event.content_ciphertext, 'UTF8')",
		"raw_event.platform = episode.source_platform",
		"raw_event.external_message_id = episode.source_message_id",
		"episode.prompt = ''",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}

func TestLegacyMemoryCleanupMigrationDropsRecordTables(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "016_drop_legacy_memory_records.sql"))
	if errorValue != nil {
		t.Fatalf("expected legacy memory cleanup migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"DROP TABLE IF EXISTS memory_source",
		"DROP TABLE IF EXISTS memory_record",
		"DROP TABLE IF EXISTS content_segment",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}

func TestConnectorQueueMigrationStoresInboxAndOutboxState(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "013_connector_queue.sql"))
	if errorValue != nil {
		t.Fatalf("expected connector queue migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"connector_event_json jsonb",
		"connector_status text",
		"connector_attempt_count integer",
		"CREATE TABLE IF NOT EXISTS connector_outbox",
		"reply_target_json jsonb",
		"reply_json jsonb",
		"UNIQUE (raw_event_id)",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}

func TestTaskAttemptMigrationStoresExecutionAuthority(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "024_task_attempt.sql"))
	if errorValue != nil {
		t.Fatalf("expected task attempt migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"CREATE TABLE IF NOT EXISTS task_attempt",
		"task_attempt_id text PRIMARY KEY",
		"task_run_id text NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE",
		"runner_id text NOT NULL",
		"ALTER TABLE task_run ADD COLUMN IF NOT EXISTS current_attempt_id text",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}

func TestCircleAccessMigrationStoresCirclePolicy(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "014_circle_access.sql"))
	if errorValue != nil {
		t.Fatalf("expected circle access migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"circles text[]",
		"CREATE TABLE IF NOT EXISTS circle",
		"CREATE TABLE IF NOT EXISTS resource_access_rule",
		"CREATE TABLE IF NOT EXISTS mattermost_circle_link",
		"scope_circle_id text",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}

func TestMemoryStoreMigrationIsTheOneBluememoOwns(t *testing.T) {
	ours, errorValue := os.ReadFile(filepath.Join("../../migrations", "030_memory_store.sql"))
	if errorValue != nil {
		t.Fatalf("expected the memory store migration to load: %v", errorValue)
	}
	theirs, errorValue := os.ReadFile(filepath.Join("../../.dependency/bluememo/migrations", "001_memory_store.sql"))
	if errorValue != nil {
		t.Fatalf("expected bluememo's migration to load: %v", errorValue)
	}
	if string(ours) != string(theirs) {
		t.Fatal("migrations/030_memory_store.sql drifted from .dependency/bluememo/migrations/001_memory_store.sql; copy the bluememo file over it, never edit it here")
	}
}
