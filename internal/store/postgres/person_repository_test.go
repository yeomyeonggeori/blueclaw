package postgres

import (
	"strings"
	"testing"
)

func TestCanonicalPersonReferenceUpdateStatementsIncludeRuntimeIdentityTables(t *testing.T) {
	statements := canonicalPersonReferenceUpdateStatements()
	expectedFragments := []string{
		"UPDATE task_schedule SET creator_person_id",
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
