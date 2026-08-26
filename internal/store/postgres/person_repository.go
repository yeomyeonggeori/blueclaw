package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type PersonRepository struct {
	database Database
}

type canonicalPersonReferenceUpdate struct {
	tableName string
	statement string
}

func NewPersonRepository(database Database) PersonRepository {
	return PersonRepository{database: database}
}

func (personRepository PersonRepository) UpsertPerson(personPolicy policy.PersonPolicy) error {
	now := time.Now().UTC()
	_, errorValue := personRepository.database.SQL.ExecContext(context.Background(), `
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
		personPolicy.GrantedClasses,
		personPolicy.Circles,
		personPolicy.IsAdmin,
		now,
	)
	if errorValue != nil {
		return errorValue
	}
	for index, email := range personPolicy.Emails {
		if errorValue := personRepository.canonicalizePersonReferencesForEmail(personPolicy.PersonID, email); errorValue != nil {
			return errorValue
		}
		if errorValue := personRepository.upsertPersonEmail(personPolicy.PersonID, email, index == 0, now); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func (personRepository PersonRepository) UpsertPeople(policyDocument policy.PolicyDocument) error {
	for _, personPolicy := range policyDocument.People {
		if errorValue := personRepository.UpsertPerson(personPolicy); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func (personRepository PersonRepository) upsertPersonEmail(personID string, email string, isPrimary bool, now time.Time) error {
	personEmailID := personID + ":" + email
	_, errorValue := personRepository.database.SQL.ExecContext(context.Background(), `
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

func (personRepository PersonRepository) canonicalizePersonReferencesForEmail(personID string, email string) error {
	row := personRepository.database.SQL.QueryRowContext(context.Background(), `
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
	return personRepository.canonicalizePersonReferences(legacyPersonID, personID)
}

func (personRepository PersonRepository) CanonicalizePersonReferences(legacyPersonID string, personID string) error {
	return personRepository.canonicalizePersonReferences(legacyPersonID, personID)
}

func (personRepository PersonRepository) canonicalizePersonReferences(legacyPersonID string, personID string) error {
	for _, updateStatement := range canonicalPersonReferenceUpdateStatements() {
		hasTable, errorValue := personRepository.hasTable(updateStatement.tableName)
		if errorValue != nil {
			return errorValue
		}
		if !hasTable {
			continue
		}
		if _, errorValue := personRepository.database.SQL.ExecContext(context.Background(), updateStatement.statement, legacyPersonID, personID); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func canonicalPersonReferenceUpdateStatements() []canonicalPersonReferenceUpdate {
	return []canonicalPersonReferenceUpdate{
		{tableName: "task_schedule", statement: "UPDATE task_schedule SET creator_person_id = $2 WHERE creator_person_id = $1"},
		{tableName: "task_run", statement: "UPDATE task_run SET requester_person_id = $2 WHERE requester_person_id = $1"},
		{tableName: "task_wait_token", statement: "UPDATE task_wait_token SET person_id = $2 WHERE person_id = $1"},
		{tableName: "task_session", statement: "UPDATE task_session SET person_id = $2 WHERE person_id = $1"},
		{tableName: "platform_account", statement: "UPDATE platform_account SET person_id = $2 WHERE person_id = $1"},
		{tableName: "raw_event", statement: "UPDATE raw_event SET sender_person_id = $2 WHERE sender_person_id = $1"},
		{tableName: "content_segment", statement: "UPDATE content_segment SET owner_person_id = $2 WHERE owner_person_id = $1"},
		{tableName: "memory_record", statement: "UPDATE memory_record SET scope_person_id = $2 WHERE scope_person_id = $1"},
		{tableName: "policy_revision", statement: "UPDATE policy_revision SET changed_by_person_id = $2 WHERE changed_by_person_id = $1"},
		{tableName: "admin_audit_log", statement: "UPDATE admin_audit_log SET actor_person_id = $2 WHERE actor_person_id = $1"},
		{tableName: "graphiti_namespace", statement: "UPDATE graphiti_namespace SET scope_person_id = $2 WHERE scope_person_id = $1"},
		{tableName: "graphiti_episode", statement: "UPDATE graphiti_episode SET sender_person_id = $2 WHERE sender_person_id = $1"},
	}
}

func (personRepository PersonRepository) hasTable(tableName string) (bool, error) {
	row := personRepository.database.SQL.QueryRowContext(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, tableName)
	var hasTable bool
	errorValue := row.Scan(&hasTable)
	return hasTable, errorValue
}
