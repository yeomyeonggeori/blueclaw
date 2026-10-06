package identity

import "testing"

func TestDirectMessageAccountRequiresOneAccountPerPlatform(t *testing.T) {
	accounts := []PlatformAccountIdentity{
		{Platform: "buzz", ExternalUserID: "z", PersonID: "person-1"},
		{Platform: "mattermost", ExternalUserID: "mattermost-1", PersonID: "person-1"},
	}
	account, isFound := DirectMessageAccount("person-1", accounts)
	if !isFound || account.Platform != "buzz" {
		t.Fatalf("expected deterministic platform selection, got %+v, %v", account, isFound)
	}
	accounts = append(accounts, PlatformAccountIdentity{Platform: "buzz", ExternalUserID: "a", PersonID: "person-1"})
	if _, isFound = DirectMessageAccount("person-1", accounts); isFound {
		t.Fatal("expected same-platform ambiguity to fail closed")
	}
}
