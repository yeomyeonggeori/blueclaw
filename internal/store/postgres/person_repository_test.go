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

// A merge used to rewrite the retired memory tables, which nothing reads any
// more. Memory moves with the file now, so a statement naming one of those
// tables is a write that goes nowhere.
func TestCanonicalPersonReferenceUpdateStatementsLeaveTheRetiredMemoryTablesAlone(t *testing.T) {
	retired := []string{"memory_fact", "memory_episode", "memory_profile", "memory_job"}
	for _, updateStatement := range canonicalPersonReferenceUpdateStatements() {
		for _, table := range retired {
			if strings.Contains(updateStatement.statement, table) {
				t.Errorf("statement writes the retired %s: %q", table, updateStatement.statement)
			}
		}
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

func TestAPolicyThatDropsAPersonRemovesThemFromLookup(t *testing.T) {
	database, _ := isolatedIntegrationDatabase(t, context.Background())
	repository := NewPersonRepository(database)
	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"), projectedPerson("person-dropped", "dropped@example.com"))

	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"))

	if personIDs := personIDsForEmail(t, database, "dropped@example.com"); len(personIDs) != 0 {
		t.Fatalf("an email of a person the policy no longer names must resolve to nobody, got %v", personIDs)
	}
	if projectedPersonIDs := projectedPersonIDs(t, database); strings.Join(projectedPersonIDs, ",") != "person-kept" {
		t.Fatalf("the projection must be exactly the policy, got %v", projectedPersonIDs)
	}
}

func TestAnEmailMovedToAnotherIDResolvesOnlyToTheCurrentID(t *testing.T) {
	database, _ := isolatedIntegrationDatabase(t, context.Background())
	repository := NewPersonRepository(database)
	replacePeople(t, repository, projectedPerson("person-before", "moved@example.com"))

	replacePeople(t, repository, projectedPerson("person-after", "moved@example.com"))

	if personIDs := personIDsForEmail(t, database, "moved@example.com"); strings.Join(personIDs, ",") != "person-after" {
		t.Fatalf("a moved email must resolve only to the id the policy names now, got %v", personIDs)
	}
	if projectedPersonIDs := projectedPersonIDs(t, database); strings.Join(projectedPersonIDs, ",") != "person-after" {
		t.Fatalf("the id the email moved away from must leave the projection, got %v", projectedPersonIDs)
	}
}

func TestAReferencedPersonsHistorySurvivesTheirRemoval(t *testing.T) {
	database, _ := isolatedIntegrationDatabase(t, context.Background())
	repository := NewPersonRepository(database)
	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"), projectedPerson("person-departed", "departed@example.com"))
	if _, errorValue := database.SQL.Exec(`INSERT INTO task_session (task_session_id, person_id, expires_at) VALUES ('session-of-departed', 'person-departed', now())`); errorValue != nil {
		t.Fatal(errorValue)
	}

	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"))

	var sessionPersonID string
	if errorValue := database.SQL.QueryRow(`SELECT person_id FROM task_session WHERE task_session_id = 'session-of-departed'`).Scan(&sessionPersonID); errorValue != nil {
		t.Fatalf("history naming a removed person must survive: %v", errorValue)
	}
	if sessionPersonID != "person-departed" {
		t.Fatalf("history must keep naming the person it was about, got %q", sessionPersonID)
	}
	if personIDs := personIDsForEmail(t, database, "departed@example.com"); len(personIDs) != 0 {
		t.Fatalf("a removed person kept for their history must never resolve from an email, got %v", personIDs)
	}
}

func projectedPerson(personID string, email string) policy.PersonPolicy {
	return policy.PersonPolicy{
		PersonID: personID, DisplayName: "이샘플", SecurityLevelName: "member", SecurityLevelRank: 10,
		Emails: []string{email},
	}
}

func replacePeople(t *testing.T, repository PersonRepository, people ...policy.PersonPolicy) {
	t.Helper()
	if errorValue := repository.ReplacePeople(policy.PolicyDocument{People: people}); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func personIDsForEmail(t *testing.T, database Database, email string) []string {
	t.Helper()
	return queryPersonIDs(t, database, `SELECT person_id FROM person_email WHERE email = $1 ORDER BY person_id`, email)
}

func projectedPersonIDs(t *testing.T, database Database) []string {
	t.Helper()
	return queryPersonIDs(t, database, `SELECT person_id FROM person ORDER BY person_id`)
}

func queryPersonIDs(t *testing.T, database Database, statement string, arguments ...any) []string {
	t.Helper()
	rows, errorValue := database.SQL.Query(statement, arguments...)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer rows.Close()
	var personIDs []string
	for rows.Next() {
		var personID string
		if errorValue := rows.Scan(&personID); errorValue != nil {
			t.Fatal(errorValue)
		}
		personIDs = append(personIDs, personID)
	}
	if errorValue := rows.Err(); errorValue != nil {
		t.Fatal(errorValue)
	}
	return personIDs
}
