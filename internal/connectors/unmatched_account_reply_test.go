package connectors

import (
	"strings"
	"testing"
)

func TestARefusalNeverClaimsSomethingItCannotRead(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "mattermost", PlatformAccountEmail: "이샘플@example.com"})

	for _, unreadableClaim := range []string{"has not invited", "not invited", "no person here is registered"} {
		if strings.Contains(reply, unreadableClaim) {
			t.Fatalf("whether someone was invited is decided in the account directory, which this process cannot read, so it cannot be asserted: %q", reply)
		}
	}
}

func TestARefusalNamesTheAddressItCouldNotMatch(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "mattermost", PlatformAccountEmail: "이샘플@example.com"})

	if !strings.Contains(reply, "이샘플@example.com") {
		t.Fatalf("an administrator cannot act on a refusal that does not say which address failed to match, got %q", reply)
	}
}

func TestARefusalOffersBothWaysTheMatchCanFail(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "slack", PlatformAccountEmail: "이샘플@example.com"})

	if !strings.Contains(reply, "different one") {
		t.Fatal("a person recorded under another address is the ordinary cause, and an administrator who is not told that will invite them again")
	}
	if !strings.Contains(reply, "slack") {
		t.Fatalf("the administrator has to know which account did not reach this Intern Kim, got %q", reply)
	}
}

func TestAnAccountWithNoAddressSaysWhatTheMatchNeeds(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "mattermost"})

	if !strings.Contains(reply, "no email address") {
		t.Fatalf("an account presenting nothing to match on cannot be told its address is unknown, got %q", reply)
	}
}
