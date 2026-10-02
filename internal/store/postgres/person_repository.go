package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const foreignKeyViolationCode = "23503"

type personStatementExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type PersonRepository struct {
	database     Database
	memoryMerger PersonMemoryMerger
}

// PersonMemoryMerger moves a person's memory when two records turn out to be
// the same person. Where that memory lives is not this repository's business,
// so it is handed in.
type PersonMemoryMerger interface {
	MergePerson(ctx context.Context, fromPersonID string, toPersonID string) error
}

type canonicalPersonReferenceUpdate struct {
	tableName string
	statement string
}

func NewPersonRepository(database Database) PersonRepository {
	return PersonRepository{database: database}
}

// NewPersonRepositoryMergingMemory is the repository a running host wants: a
// merge moves the person's memory as well as the rows that name them.
func NewPersonRepositoryMergingMemory(database Database, memoryMerger PersonMemoryMerger) PersonRepository {
	return PersonRepository{database: database, memoryMerger: memoryMerger}
}

func (personRepository PersonRepository) UpsertPerson(personPolicy policy.PersonPolicy) error {
	return personRepository.upsertPerson(context.Background(), personRepository.database.SQL, personPolicy)
}

func (personRepository PersonRepository) ReplacePeople(policyDocument policy.PolicyDocument) error {
	ctx := context.Background()
	transaction, errorValue := personRepository.database.SQL.BeginTx(ctx, nil)
	if errorValue != nil {
		return errorValue
	}
	defer transaction.Rollback()
	for _, personPolicy := range policyDocument.People {
		if errorValue := personRepository.upsertPerson(ctx, transaction, personPolicy); errorValue != nil {
			return errorValue
		}
	}
	if errorValue := retirePeopleOutside(ctx, transaction, namedPersonIDs(policyDocument)); errorValue != nil {
		return errorValue
	}
	return transaction.Commit()
}

func namedPersonIDs(policyDocument policy.PolicyDocument) []string {
	personIDs := make([]string, 0, len(policyDocument.People))
	for _, personPolicy := range policyDocument.People {
		personIDs = append(personIDs, personPolicy.PersonID)
	}
	return personIDs
}

func retirePeopleOutside(ctx context.Context, transaction *sql.Tx, namedPersonIDs []string) error {
	stalePersonIDs, errorValue := personIDsOutside(ctx, transaction, namedPersonIDs)
	if errorValue != nil {
		return errorValue
	}
	for _, personID := range stalePersonIDs {
		if errorValue := retirePerson(ctx, transaction, personID); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func personIDsOutside(ctx context.Context, transaction *sql.Tx, namedPersonIDs []string) ([]string, error) {
	rows, errorValue := transaction.QueryContext(ctx, `SELECT person_id FROM person WHERE person_id <> ALL($1)`, namedPersonIDs)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	var personIDs []string
	for rows.Next() {
		var personID string
		if errorValue := rows.Scan(&personID); errorValue != nil {
			return nil, errorValue
		}
		personIDs = append(personIDs, personID)
	}
	return personIDs, rows.Err()
}

func retirePerson(ctx context.Context, transaction *sql.Tx, personID string) error {
	if errorValue := stopSchedulesOf(ctx, transaction, personID); errorValue != nil {
		return errorValue
	}
	if _, errorValue := transaction.ExecContext(ctx, `DELETE FROM person_email WHERE person_id = $1`, personID); errorValue != nil {
		return errorValue
	}
	if _, errorValue := transaction.ExecContext(ctx, `SAVEPOINT retire_person`); errorValue != nil {
		return errorValue
	}
	_, deleteError := transaction.ExecContext(ctx, `DELETE FROM person WHERE person_id = $1`, personID)
	if isForeignKeyViolation(deleteError) {
		_, errorValue := transaction.ExecContext(ctx, `ROLLBACK TO SAVEPOINT retire_person`)
		return errorValue
	}
	if deleteError != nil {
		return deleteError
	}
	_, errorValue := transaction.ExecContext(ctx, `RELEASE SAVEPOINT retire_person`)
	return errorValue
}

func stopSchedulesOf(ctx context.Context, transaction *sql.Tx, personID string) error {
	stoppedAt := time.Now().UTC()
	if _, errorValue := cancelSchedules(ctx, transaction, task.ScheduleCancelRequest{
		Scope: task.ScheduleCancelScopeMine, RequesterPersonID: personID, CancelledAt: stoppedAt,
	}); errorValue != nil {
		return errorValue
	}
	return deactivateMorningBriefing(ctx, transaction, personID, stoppedAt)
}

func isForeignKeyViolation(errorValue error) bool {
	var postgresError *pgconn.PgError
	return errors.As(errorValue, &postgresError) && postgresError.Code == foreignKeyViolationCode
}

func (personRepository PersonRepository) upsertPerson(ctx context.Context, executor personStatementExecutor, personPolicy policy.PersonPolicy) error {
	now := time.Now().UTC()
	_, errorValue := executor.ExecContext(ctx, `
INSERT INTO person (
  person_id, display_name, security_level_name, security_level_rank,
  granted_classes, circles, is_admin, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
ON CONFLICT (person_id) DO UPDATE SET
  display_name = EXCLUDED.display_name,
  security_level_name = EXCLUDED.security_level_name,
  security_level_rank = EXCLUDED.security_level_rank,
  granted_classes = EXCLUDED.granted_classes,
  circles = EXCLUDED.circles,
  is_admin = EXCLUDED.is_admin,
  updated_at = EXCLUDED.updated_at`,
		personPolicy.PersonID,
		personPolicy.DisplayName,
		personPolicy.SecurityLevelName,
		personPolicy.SecurityLevelRank,
		emptyWhenNil(personPolicy.GrantedClasses),
		emptyWhenNil(personPolicy.Circles),
		personPolicy.IsAdmin,
		now,
	)
	if errorValue != nil {
		return errorValue
	}
	for index, email := range personPolicy.Emails {
		if errorValue := personRepository.canonicalizePersonReferencesForEmail(ctx, executor, personPolicy.PersonID, email); errorValue != nil {
			return errorValue
		}
		if errorValue := upsertPersonEmail(ctx, executor, personPolicy.PersonID, email, index == 0, now); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func upsertPersonEmail(ctx context.Context, executor personStatementExecutor, personID string, email string, isPrimary bool, now time.Time) error {
	personEmailID := personID + ":" + email
	_, errorValue := executor.ExecContext(ctx, `
INSERT INTO person_email (person_email_id, person_id, email, is_primary, created_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (email) DO UPDATE SET
  person_id = EXCLUDED.person_id,
  is_primary = EXCLUDED.is_primary`,
		personEmailID,
		personID,
		email,
		isPrimary,
		now,
	)
	return errorValue
}

func (personRepository PersonRepository) canonicalizePersonReferencesForEmail(ctx context.Context, executor personStatementExecutor, personID string, email string) error {
	row := executor.QueryRowContext(ctx, `
SELECT person_id FROM person_email WHERE email = $1`, email)
	var legacyPersonID string
	errorValue := row.Scan(&legacyPersonID)
	if errorValue == sql.ErrNoRows {
		return nil
	}
	if errorValue != nil {
		return errorValue
	}
	if legacyPersonID == "" || legacyPersonID == personID {
		return nil
	}
	return personRepository.canonicalizePersonReferences(ctx, executor, legacyPersonID, personID)
}

func (personRepository PersonRepository) CanonicalizePersonReferences(legacyPersonID string, personID string) error {
	return personRepository.canonicalizePersonReferences(context.Background(), personRepository.database.SQL, legacyPersonID, personID)
}

func (personRepository PersonRepository) canonicalizePersonReferences(ctx context.Context, executor personStatementExecutor, legacyPersonID string, personID string) error {
	for _, updateStatement := range canonicalPersonReferenceUpdateStatements() {
		hasTable, errorValue := tableExists(ctx, executor, updateStatement.tableName)
		if errorValue != nil {
			return errorValue
		}
		if !hasTable {
			continue
		}
		if _, errorValue := executor.ExecContext(ctx, updateStatement.statement, legacyPersonID, personID); errorValue != nil {
			return errorValue
		}
	}
	if personRepository.memoryMerger == nil {
		return nil
	}
	return personRepository.memoryMerger.MergePerson(ctx, legacyPersonID, personID)
}

func canonicalPersonReferenceUpdateStatements() []canonicalPersonReferenceUpdate {
	return []canonicalPersonReferenceUpdate{
		{tableName: "schedule", statement: "UPDATE schedule SET creator_person_id = $2 WHERE creator_person_id = $1"},
		{tableName: "task_run", statement: "UPDATE task_run SET requester_person_id = $2 WHERE requester_person_id = $1"},
		{tableName: "task_wait_token", statement: "UPDATE task_wait_token SET person_id = $2 WHERE person_id = $1"},
		{tableName: "task_session", statement: "UPDATE task_session SET person_id = $2 WHERE person_id = $1"},
		{tableName: "platform_account", statement: "UPDATE platform_account SET person_id = $2 WHERE person_id = $1"},
		{tableName: "raw_event", statement: "UPDATE raw_event SET sender_person_id = $2 WHERE sender_person_id = $1"},
		{tableName: "content_segment", statement: "UPDATE content_segment SET owner_person_id = $2 WHERE owner_person_id = $1"},
		{tableName: "memory_record", statement: "UPDATE memory_record SET scope_person_id = $2 WHERE scope_person_id = $1"},
		{tableName: "policy_revision", statement: "UPDATE policy_revision SET changed_by_person_id = $2 WHERE changed_by_person_id = $1"},
		{tableName: "admin_audit_log", statement: "UPDATE admin_audit_log SET actor_person_id = $2 WHERE actor_person_id = $1"},
	}
}

func tableExists(ctx context.Context, executor personStatementExecutor, tableName string) (bool, error) {
	row := executor.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, tableName)
	var hasTable bool
	errorValue := row.Scan(&hasTable)
	return hasTable, errorValue
}

func emptyWhenNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
