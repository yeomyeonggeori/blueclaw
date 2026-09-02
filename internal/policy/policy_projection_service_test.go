package policy

import (
	"sort"
	"strings"
	"testing"
)

func TestPolicyProjectionGivesMemberToEveryPerson(t *testing.T) {
	policyProjection := PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(PolicyDocument{
		People: []PersonPolicy{
			{
				PersonID: "person-1",
				Emails:   []string{"person@example.com"},
			},
			{
				PersonID: "person-2",
				Emails:   []string{"second@example.com"},
				Circles:  []string{"finance"},
			},
			{
				PersonID: "admin-1",
				Emails:   []string{"admin@example.com"},
				IsAdmin:  true,
			},
		},
	})

	for _, personID := range []string{"person-1", "person-2", "admin-1"} {
		personAccess := policyProjection.PersonAccessByPersonID[personID]
		if !hasTestPolicyString(personAccess.Circles, "member") {
			t.Fatalf("expected %s to have member circle, got %+v", personID, personAccess.Circles)
		}
	}
}

func TestPolicyProjectionAddsAdminWithoutGrantingCLevel(t *testing.T) {
	policyProjection := PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(PolicyDocument{
		People: []PersonPolicy{{
			PersonID:       "admin-1",
			Emails:         []string{"admin@example.com"},
			IsAdmin:        true,
			GrantedClasses: []string{"internal", "executive"},
		}},
	})

	personAccess := policyProjection.PersonAccessByPersonID["admin-1"]
	if !hasTestPolicyString(personAccess.Circles, "member") || !hasTestPolicyString(personAccess.Circles, "admin") {
		t.Fatalf("expected member and admin circles, got %+v", personAccess.Circles)
	}
	if hasTestPolicyString(personAccess.Circles, "c-level") {
		t.Fatalf("expected executive legacy class not to grant c-level, got %+v", personAccess.Circles)
	}
}

func TestPolicyProjectionNormalizesExplicitCircles(t *testing.T) {
	policyProjection := PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(PolicyDocument{
		ResourceAccess: []ResourceAccessPolicy{{
			Resource: "tool:company_broadcast_send",
			Actions:  []string{"execute"},
			Circles:  []string{"representative"},
		}},
		People: []PersonPolicy{{
			PersonID: "person-1",
			Circles:  []string{" Member ", "Finance", "finance"},
		}},
	})

	personAccess := policyProjection.PersonAccessByPersonID["person-1"]
	if len(personAccess.Circles) != 2 || personAccess.Circles[0] != "member" || personAccess.Circles[1] != "finance" {
		t.Fatalf("expected normalized unique circles, got %+v", personAccess.Circles)
	}
	if len(personAccess.ResourceAccessRules) != 1 {
		t.Fatalf("expected resource access rules copied to person access, got %+v", personAccess.ResourceAccessRules)
	}
}

func TestEnsureRequesterDefaultsPreservesMemberInvariant(t *testing.T) {
	personAccess := EnsureRequesterDefaults(PersonAccess{
		PersonID: "person-1",
		Circles:  []string{" Finance ", "member", "finance"},
	})

	if len(personAccess.Circles) != 2 || personAccess.Circles[0] != "member" || personAccess.Circles[1] != "finance" {
		t.Fatalf("expected member plus normalized explicit circles, got %+v", personAccess.Circles)
	}
}

func TestCanonicalizePolicyDocumentWritesMemberToEveryPersonAndCircleList(t *testing.T) {
	policyDocument := CanonicalizePolicyDocument(PolicyDocument{
		People: []PersonPolicy{
			{PersonID: "person-1", Emails: []string{"person@example.com"}, Circles: []string{"Finance"}},
			{PersonID: "admin-1", Emails: []string{"admin@example.com"}, IsAdmin: true},
		},
		Circles: []CirclePolicy{{CircleID: "Finance", DisplayName: "Finance"}},
	})

	if !hasTestPolicyCircle(policyDocument.Circles, "member") {
		t.Fatalf("expected canonical policy to include member circle, got %+v", policyDocument.Circles)
	}
	for _, personPolicy := range policyDocument.People {
		if !hasTestPolicyString(personPolicy.Circles, "member") {
			t.Fatalf("expected %s to persist member membership, got %+v", personPolicy.PersonID, personPolicy.Circles)
		}
	}
	if !hasTestPolicyString(policyDocument.People[1].Circles, "admin") {
		t.Fatalf("expected admin person to persist admin membership, got %+v", policyDocument.People[1].Circles)
	}
}

func hasTestPolicyCircle(circles []CirclePolicy, expectedCircleID string) bool {
	for _, circle := range circles {
		if circle.CircleID == expectedCircleID {
			return true
		}
	}
	return false
}

func hasTestPolicyString(values []string, expectedValue string) bool {
	for _, value := range values {
		if value == expectedValue {
			return true
		}
	}
	return false
}

func TestContainedCirclesFollowMemberCirclesOnTheCirclePolicy(t *testing.T) {
	contained := ContainedCircles([]CirclePolicy{
		{CircleID: "Engineering", MemberCircles: []string{"Platform", "data", "platform", ""}},
		{CircleID: "sales"},
	})
	engineering := append([]string{}, contained["engineering"]...)
	sort.Strings(engineering)
	if len(contained) != 1 || strings.Join(engineering, ",") != "data,platform" {
		t.Fatalf("expected engineering to contain data and platform once, lowercased, got %v", contained)
	}
	projection := PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(PolicyDocument{Circles: []CirclePolicy{{CircleID: "engineering", MemberCircles: []string{"platform"}}}})
	if strings.Join(projection.ContainedCirclesByID["engineering"], ",") != "platform" {
		t.Fatalf("expected the projection to carry the containment, got %v", projection.ContainedCirclesByID)
	}
}
