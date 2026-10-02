package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type testPlatformAccountRepository struct {
	platformAccounts []PlatformAccountIdentity
}

func (repository testPlatformAccountRepository) UpsertPlatformAccount(PlatformAccountIdentity) error {
	return nil
}

func (repository testPlatformAccountRepository) ListPlatformAccount() ([]PlatformAccountIdentity, error) {
	return append([]PlatformAccountIdentity{}, repository.platformAccounts...), nil
}

func TestIdentityServiceResolvesPersonDisplayNameByPersonID(t *testing.T) {
	identityService := NewIdentityService(policy.PolicyProjection{
		DisplayNameByPersonID: map[string]string{"person-rain": "김테스트"},
	})
	if name := identityService.ResolvePersonDisplayName("person-rain"); name != "김테스트" {
		t.Fatalf("display name = %q, want 김테스트", name)
	}
	if name := identityService.ResolvePersonDisplayName("person-unknown"); name != "" {
		t.Fatalf("unknown person display name = %q, want empty", name)
	}
}

func TestIdentityServiceProjectionServiceCarriesDisplayName(t *testing.T) {
	projection := policy.PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(policy.PolicyDocument{
		People: []policy.PersonPolicy{{PersonID: "person-rain", DisplayName: "김테스트", Emails: []string{"rain@example.com"}}},
	})
	if projection.DisplayNameByPersonID["person-rain"] != "김테스트" {
		t.Fatalf("projection display name = %q", projection.DisplayNameByPersonID["person-rain"])
	}
}

func TestIdentityServiceReloadsPlatformAccountToCurrentPolicyPersonByEmail(t *testing.T) {
	identityService := NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{
			"user@example.com": "current-person",
		},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"current-person": {PersonID: "current-person"},
		},
	})
	identityService.UsePlatformAccountRepository(testPlatformAccountRepository{
		platformAccounts: []PlatformAccountIdentity{{
			Platform:       "mattermost",
			ExternalUserID: "mattermost-user",
			Email:          "user@example.com",
			PersonID:       "stale-person",
		}},
	})

	personID, isFound := identityService.ResolvePersonIDByPlatformAccount("mattermost", "mattermost-user")
	if !isFound || personID != "current-person" {
		t.Fatalf("expected current policy person, got personID=%q found=%v", personID, isFound)
	}
}

func TestIdentityServiceSkipsStalePlatformAccountWithoutPolicyEmail(t *testing.T) {
	identityService := NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"current-person": {PersonID: "current-person"},
		},
	})
	identityService.UsePlatformAccountRepository(testPlatformAccountRepository{
		platformAccounts: []PlatformAccountIdentity{{
			Platform:       "mattermost",
			ExternalUserID: "mattermost-user",
			Email:          "removed@example.com",
			PersonID:       "stale-person",
		}},
	})

	_, isFound := identityService.ResolvePersonIDByPlatformAccount("mattermost", "mattermost-user")
	if isFound {
		t.Fatal("expected stale platform account to be ignored")
	}
}

func TestIdentityServiceResolvesRequesterAccessWithMember(t *testing.T) {
	identityService := NewIdentityService(policy.PolicyProjection{
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1", Circles: []string{"finance"}},
		},
	})

	personAccess := identityService.ResolvePersonAccess("person-1")

	if !hasIdentityTestString(personAccess.Circles, "member") || !hasIdentityTestString(personAccess.Circles, "finance") {
		t.Fatalf("expected requester access to include member and explicit circles, got %+v", personAccess.Circles)
	}
}

func hasIdentityTestString(values []string, expectedValue string) bool {
	for _, value := range values {
		if value == expectedValue {
			return true
		}
	}
	return false
}

func TestIdentityServiceExposesContainedCircles(t *testing.T) {
	identityService := NewIdentityService(policy.PolicyProjection{ContainedCirclesByID: map[string][]string{"engineering": {"platform"}}})
	contained := identityService.ContainedCircles()
	contained["engineering"] = append(contained["engineering"], "tampered")
	if len(identityService.ContainedCircles()["engineering"]) != 1 {
		t.Fatal("expected the identity service to hand out a copy of the containment map")
	}
}

func TestAwaitingAPersonReturnsOnceTheRosterNamesThem(t *testing.T) {
	identityService := NewIdentityService(policy.PolicyProjection{})
	found := make(chan string, 1)
	go func() {
		personID, _ := identityService.AwaitPersonIDByEmail(context.Background(), "Late@Example.com")
		found <- personID
	}()
	select {
	case personID := <-found:
		t.Fatalf("the wait returned %q before the roster named anybody", personID)
	case <-time.After(100 * time.Millisecond):
	}
	identityService.ReloadPolicyProjection(policy.PolicyProjection{PersonIDByEmail: map[string]string{"late@example.com": "person-late"}})
	select {
	case personID := <-found:
		if personID != "person-late" {
			t.Fatalf("the wait returned %q, want person-late", personID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not return after the roster named the person")
	}
}

func TestAwaitingAPersonTheRosterNeverNamesEndsWithTheCaller(t *testing.T) {
	identityService := NewIdentityService(policy.PolicyProjection{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	identityService.ReloadPolicyProjection(policy.PolicyProjection{PersonIDByEmail: map[string]string{"other@example.com": "person-other"}})
	if _, errorValue := identityService.AwaitPersonIDByEmail(ctx, "stranger@example.com"); !errors.Is(errorValue, context.DeadlineExceeded) {
		t.Fatalf("the wait ended with %v, want the caller's deadline", errorValue)
	}
}
