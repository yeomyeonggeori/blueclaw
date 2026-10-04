package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
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

func TestAPersonWithoutCirclesIsProjected(t *testing.T) {
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
		PersonID: personID, DisplayName: "Alex",
	}); errorValue != nil {
		t.Fatalf("a person the policy names without optional lists must be stored: %v", errorValue)
	}
	var circleCount int
	if errorValue := database.SQL.QueryRow(`SELECT cardinality(circles) FROM person WHERE person_id = $1`, personID).Scan(&circleCount); errorValue != nil {
		t.Fatal(errorValue)
	}
	if circleCount != 0 {
		t.Fatalf("expected no circles, got %d", circleCount)
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
		PersonID: personID, DisplayName: "이샘플",
		Emails: []string{email},
	}
}

func replacePeople(t *testing.T, repository PersonRepository, people ...policy.PersonPolicy) {
	t.Helper()
	if errorValue := repository.ReplacePeople(policy.PolicyDocument{People: append([]policy.PersonPolicy{}, people...)}); errorValue != nil {
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

func TestAPolicyThatDropsAPersonStopsTheirSchedulesAndBriefing(t *testing.T) {
	database, _ := isolatedIntegrationDatabase(t, context.Background())
	repository := NewPersonRepository(database)
	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"), projectedPerson("person-departed", "departed@example.com"))
	upsertDueSchedule(t, database, "schedule-of-kept", "person-kept", "")
	upsertDueSchedule(t, database, "schedule-of-departed", "person-departed", "")
	reconcileDueMorningBriefing(t, database, "person-departed")

	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"))

	if claimedScheduleIDs := claimDueScheduleIDs(t, database); strings.Join(claimedScheduleIDs, ",") != "schedule-of-kept" {
		t.Fatalf("only the remaining person's schedule may fire, and it must still fire, got %v", claimedScheduleIDs)
	}
}

func TestAStoppedScheduleKeepsItsRunHistory(t *testing.T) {
	database, _ := isolatedIntegrationDatabase(t, context.Background())
	repository := NewPersonRepository(database)
	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"), projectedPerson("person-departed", "departed@example.com"))
	if _, errorValue := database.SQL.Exec(`
INSERT INTO task_run (task_run_id, requester_person_id, current_agent_profile_name, status, prompt, created_at, updated_at)
VALUES ('run-of-departed', 'person-departed', 'default', 'completed', 'scheduled', now(), now())`); errorValue != nil {
		t.Fatal(errorValue)
	}
	upsertDueSchedule(t, database, "schedule-of-departed", "person-departed", "run-of-departed")

	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"))

	var lastTaskRunID string
	var isCancelled bool
	if errorValue := database.SQL.QueryRow(`
SELECT last_task_run_id, expires_at IS NOT NULL AND next_run_at IS NULL FROM schedule WHERE schedule_id = 'schedule-of-departed'`).Scan(&lastTaskRunID, &isCancelled); errorValue != nil {
		t.Fatalf("a stopped schedule must stay on record: %v", errorValue)
	}
	if !isCancelled {
		t.Fatal("a departed person's schedule must be cancelled the way its owner would cancel it")
	}
	if lastTaskRunID != "run-of-departed" {
		t.Fatalf("a stopped schedule must keep naming its last run, got %q", lastTaskRunID)
	}
	var requesterPersonID string
	if errorValue := database.SQL.QueryRow(`SELECT requester_person_id FROM task_run WHERE task_run_id = 'run-of-departed'`).Scan(&requesterPersonID); errorValue != nil {
		t.Fatalf("the run a stopped schedule made must survive: %v", errorValue)
	}
	if requesterPersonID != "person-departed" {
		t.Fatalf("the run must keep naming who it ran for, got %q", requesterPersonID)
	}
}

func TestAReturningPersonsSchedulesStayStopped(t *testing.T) {
	database, _ := isolatedIntegrationDatabase(t, context.Background())
	repository := NewPersonRepository(database)
	replacePeople(t, repository, projectedPerson("person-returning", "returning@example.com"))
	upsertDueSchedule(t, database, "schedule-of-returning", "person-returning", "")
	replacePeople(t, repository)

	replacePeople(t, repository, projectedPerson("person-returning", "returning@example.com"))

	if claimedScheduleIDs := claimDueScheduleIDs(t, database); len(claimedScheduleIDs) != 0 {
		t.Fatalf("a schedule stopped by a departure must not come back with the person, got %v", claimedScheduleIDs)
	}
}

func upsertDueSchedule(t *testing.T, database Database, scheduleID string, personID string, lastTaskRunID string) {
	t.Helper()
	dueAt := time.Now().UTC().Add(-time.Minute)
	schedule := morningBriefingTestSchedule(personID, scheduleID, &dueAt)
	schedule.Name = "Weekly report"
	schedule.LastTaskRunID = lastTaskRunID
	schedule.CreatedAt = dueAt
	schedule.UpdatedAt = dueAt
	if errorValue := NewScheduleRepository(database).UpsertSchedule(schedule); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func reconcileDueMorningBriefing(t *testing.T, database Database, personID string) {
	t.Helper()
	dueAt := time.Now().UTC().Add(-time.Minute)
	briefing := morningBriefingTestSchedule(personID, task.MorningBriefingScheduleID(personID), &dueAt)
	briefing.CreatedAt = dueAt
	if errorValue := NewScheduleRepository(database).ReconcileMorningBriefings(context.Background(), []task.Schedule{briefing}, nil, dueAt); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func claimDueScheduleIDs(t *testing.T, database Database) []string {
	t.Helper()
	schedules, errorValue := NewScheduleRepository(database).ClaimDueSchedules(10, time.Minute, time.Now().UTC(), "departure-test")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	scheduleIDs := make([]string, 0, len(schedules))
	for _, schedule := range schedules {
		scheduleIDs = append(scheduleIDs, schedule.ScheduleID)
	}
	return scheduleIDs
}

func TestARosterNotYetHandedOverRetiresNobody(t *testing.T) {
	database, _ := isolatedIntegrationDatabase(t, context.Background())
	repository := NewPersonRepository(database)
	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"))
	upsertDueSchedule(t, database, "schedule-of-kept", "person-kept", "")
	reconcileDueMorningBriefing(t, database, "person-kept")
	var unreceivedRoster policy.PolicyDocument
	if errorValue := json.Unmarshal([]byte(`{"circles":[],"circleSync":{},"resourceAccess":[],"channels":[],"retention":{}}`), &unreceivedRoster); errorValue != nil {
		t.Fatal(errorValue)
	}

	if errorValue := repository.ReplacePeople(unreceivedRoster); errorValue != nil {
		t.Fatalf("a roster nobody has handed over yet must project without error: %v", errorValue)
	}

	if personIDs := personIDsForEmail(t, database, "kept@example.com"); strings.Join(personIDs, ",") != "person-kept" {
		t.Fatalf("a roster that names no people must leave the projected ones resolvable, got %v", personIDs)
	}
	claimedScheduleIDs := claimDueScheduleIDs(t, database)
	if strings.Join(claimedScheduleIDs, ",") != "schedule-of-kept,"+task.MorningBriefingScheduleID("person-kept") {
		t.Fatalf("a roster that names no people must stop nobody's schedules, got %v", claimedScheduleIDs)
	}
}

func TestADepartedPersonsBriefingKeepsThemOnRecordWhicheverKeyRefusesFirst(t *testing.T) {
	database, _ := isolatedIntegrationDatabase(t, context.Background())
	repository := NewPersonRepository(database)
	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"), projectedPerson("person-departed", "departed@example.com"))
	reconcileDueMorningBriefing(t, database, "person-departed")
	checkTheBriefingKeyFirst(t, database)

	replacePeople(t, repository, projectedPerson("person-kept", "kept@example.com"))

	if projectedPersonIDs := projectedPersonIDs(t, database); strings.Join(projectedPersonIDs, ",") != "person-departed,person-kept" {
		t.Fatalf("a departed person their briefing still names must stay on record, got %v", projectedPersonIDs)
	}
	if personIDs := personIDsForEmail(t, database, "departed@example.com"); len(personIDs) != 0 {
		t.Fatalf("a departed person kept for their briefing must never resolve from an email, got %v", personIDs)
	}
	if claimedScheduleIDs := claimDueScheduleIDs(t, database); len(claimedScheduleIDs) != 0 {
		t.Fatalf("a departed person's briefing must stop, got %v", claimedScheduleIDs)
	}
}

// PostgreSQL fires a table's referential triggers in name order, and each name
// carries its constraint's OID; pg_restore creates foreign keys table by table
// in name order, so a restored database checks morning_briefing_schedule's key
// before schedule's.
func checkTheBriefingKeyFirst(t *testing.T, database Database) {
	t.Helper()
	if _, errorValue := database.SQL.Exec(`
ALTER TABLE schedule DROP CONSTRAINT schedule_creator_person_id_fkey;
ALTER TABLE schedule ADD CONSTRAINT schedule_creator_person_id_fkey FOREIGN KEY (creator_person_id) REFERENCES person(person_id)`); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestEitherWayPostgresRefusesADeleteForAReferenceKeepsThePerson(t *testing.T) {
	for _, code := range []string{foreignKeyViolationCode, restrictViolationCode} {
		if !isRefusedByAReference(&pgconn.PgError{Code: code}) {
			t.Errorf("SQLSTATE %s refuses a delete because a row still references the person", code)
		}
	}
	if isRefusedByAReference(&pgconn.PgError{Code: "23505"}) {
		t.Error("a unique violation is not a reference refusing the delete")
	}
}
