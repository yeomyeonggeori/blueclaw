package adminapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func TestInvitePersonRequiresPersonID(t *testing.T) {
	policyPath := writePolicyHandlerTestPolicy(t, policyHandlerTestPolicy())
	handler := PolicyHandler{
		PolicyPath:    policyPath,
		PolicyLoader:  policy.PolicyLoader{},
		PolicySaver:   policy.PolicySaver{},
		PolicyWatcher: &policy.PolicyWatcher{},
		Validator:     policy.PolicyValidator{},
		AuditHandler:  NewAuditHandler(),
	}

	_, _, _, errorValue := handler.invitePerson(invitePersonRequest{Email: "person@example.com"})

	if errorValue == nil || errorValue.Error() != "personID is required" {
		t.Fatalf("expected personID error, got %v", errorValue)
	}
}

func TestInvitePersonRekeysExistingEmailToCanonicalPersonID(t *testing.T) {
	policyDocument := policyHandlerTestPolicy()
	policyDocument.People = []policy.PersonPolicy{{
		PersonID:    "legacy-person",
		DisplayName: "Lee",
		Emails:      []string{"lee@example.com"},
	}}
	policyPath := writePolicyHandlerTestPolicy(t, policyDocument)
	handler := PolicyHandler{
		PolicyPath:    policyPath,
		PolicyLoader:  policy.PolicyLoader{},
		PolicySaver:   policy.PolicySaver{},
		PolicyWatcher: &policy.PolicyWatcher{},
		Validator:     policy.PolicyValidator{},
		AuditHandler:  NewAuditHandler(),
	}

	policyDocument, personPolicy, _, errorValue := handler.invitePerson(invitePersonRequest{
		PersonID: "user-1",
		Email:    "lee@example.com",
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if personPolicy.PersonID != "user-1" {
		t.Fatalf("personID = %q", personPolicy.PersonID)
	}
	if policyDocument.People[0].PersonID != "user-1" {
		t.Fatalf("policy personID = %q", policyDocument.People[0].PersonID)
	}
}

func policyHandlerTestPolicy() policy.PolicyDocument {
	return policy.PolicyDocument{Retention: policy.RetentionPolicy{RawEventDays: 60}}
}

func writePolicyHandlerTestPolicy(t *testing.T, policyDocument policy.PolicyDocument) string {
	t.Helper()
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	document, errorValue := json.Marshal(policy.CanonicalizePolicyDocument(policyDocument))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(policyPath, document, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	return policyPath
}
