package postgres

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func TestCanonicalPersonReferenceUpdateStatementsIncludeRuntimeIdentityTables(t *testing.T) {
	statements := canonicalPersonReferenceUpdateStatements()
	expectedFragments := []string{
		"UPDATE schedule SET creator_person_id",
		"UPDATE task_run SET requester_person_id",
		"UPDATE platform_account SET person_id",
		"UPDATE memory_record SET scope_person_id",
		"UPDATE memory_fact SET subject_person_id",
	}

	for _, fragment := range expectedFragments {
		if !containsCanonicalPersonReferenceStatement(statements, fragment) {
			t.Fatalf("missing canonical person reference update for %q", fragment)
		}
	}
	if hasDuplicateCanonicalPersonReferenceStatements(statements) {
		t.Fatalf("duplicate canonical person reference statement in %#v", statements)
	}
}

func containsCanonicalPersonReferenceStatement(statements []canonicalPersonReferenceUpdate, fragment string) bool {
	for _, updateStatement := range statements {
		if strings.Contains(updateStatement.statement, fragment) {
			return true
		}
	}
	return false
}

func hasDuplicateCanonicalPersonReferenceStatements(statements []canonicalPersonReferenceUpdate) bool {
	seenStatement := map[string]bool{}
	for _, updateStatement := range statements {
		if seenStatement[updateStatement.statement] {
			return true
		}
		seenStatement[updateStatement.statement] = true
	}
	return false
}

func TestAPersonWithoutGrantedClassesOrCirclesIsProjected(t *testing.T) {
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("set BLUECLAW_TEST_POSTGRES_URL to run the disposable PostgreSQL regression")
	}
	database, errorValue := OpenDatabase(context.Background(), connectionString, 0)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer database.Close()
	migrationRunner := MigrationRunner{MigrationDirectoryPath: filepath.Join("..", "..", "..", "migrations")}
	if errorValue := migrationRunner.ApplyMigrations(context.Background(), database); errorValue != nil {
		t.Fatal(errorValue)
	}
	personID := "projection-test-" + time.Now().UTC().Format("20060102150405.000000000")
	defer database.SQL.Exec(`DELETE FROM person WHERE person_id = $1`, personID)

	if errorValue := NewPersonRepository(database).UpsertPerson(policy.PersonPolicy{
		PersonID: personID, DisplayName: "Alex", SecurityLevelName: "member", SecurityLevelRank: 10,
	}); errorValue != nil {
		t.Fatalf("a person the policy names without optional lists must be stored: %v", errorValue)
	}
	var grantedClassCount int
	if errorValue := database.SQL.QueryRow(`SELECT cardinality(granted_classes) FROM person WHERE person_id = $1`, personID).Scan(&grantedClassCount); errorValue != nil {
		t.Fatal(errorValue)
	}
	if grantedClassCount != 0 {
		t.Fatalf("expected no granted classes, got %d", grantedClassCount)
	}
}
