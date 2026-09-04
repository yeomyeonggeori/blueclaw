package connectors

import (
	"strings"
	"testing"
)

func TestARefusalNeverClaimsSomethingItCannotRead(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "mattermost", PlatformAccountEmail: "이샘플@example.com"}, "en")

	for _, unreadableClaim := range []string{"has not invited", "not invited", "no person here is registered"} {
		if strings.Contains(reply, unreadableClaim) {
			t.Fatalf("whether someone was invited is decided in the account directory, which this process cannot read, so it cannot be asserted: %q", reply)
		}
	}
}

func TestARefusalNamesTheAddressItCouldNotMatch(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "mattermost", PlatformAccountEmail: "이샘플@example.com"}, "en")

	if !strings.Contains(reply, "이샘플@example.com") {
		t.Fatalf("an administrator cannot act on a refusal that does not say which address failed to match, got %q", reply)
	}
}

func TestARefusalOffersBothWaysTheMatchCanFail(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "slack", PlatformAccountEmail: "이샘플@example.com"}, "en")

	if !strings.Contains(reply, "different one") {
		t.Fatal("a person recorded under another address is the ordinary cause, and an administrator who is not told that will invite them again")
	}
	if !strings.Contains(reply, "slack") {
		t.Fatalf("the administrator has to know which account did not reach this Intern Kim, got %q", reply)
	}
}

func TestAnAccountWithNoAddressSaysWhatTheMatchNeeds(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "mattermost"}, "en")

	if !strings.Contains(reply, "no email address") {
		t.Fatalf("an account presenting nothing to match on cannot be told its address is unknown, got %q", reply)
	}
}

func TestAKoreanCompanysStrangerGetsKorean(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "mattermost", PlatformAccountEmail: "이샘플@example.com"}, "ko")

	if !strings.Contains(reply, "관리자") {
		t.Fatalf("a Korean company answers a stranger in Korean, same as every other fixed product string, got %q", reply)
	}
	if strings.Contains(reply, "administrator") {
		t.Fatalf("a Korean reply should not also carry the English sentence, got %q", reply)
	}
}

func TestAnUnsetCompanyLocaleStaysEnglish(t *testing.T) {
	reply := unmatchedAccountReplyFor(senderAuthorization{Platform: "mattermost", PlatformAccountEmail: "이샘플@example.com"}, "")

	if !strings.Contains(reply, "administrator") {
		t.Fatalf("a company that has never set a locale keeps answering exactly as this reply always has, got %q", reply)
	}
}
