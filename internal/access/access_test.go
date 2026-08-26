package access

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func TestMemberCanAccessMemberCircleFile(t *testing.T) {
	personAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}}
	resource := "file:circle:member"

	if !CanAccess(Request{PersonAccess: personAccess, Action: ActionWrite, Resource: resource}) {
		t.Fatal("a member should write member circle files")
	}
}

func TestCircleMemberCanAccessCircleFile(t *testing.T) {
	personAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"member", "finance"}}
	resource := "file:circle:finance"

	if !CanAccess(Request{PersonAccess: personAccess, Action: ActionRead, Resource: resource}) {
		t.Fatal("finance member should read finance circle files")
	}
}

func TestCircleNonMemberCannotAccessCircleFile(t *testing.T) {
	personAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}}
	resource := "file:circle:finance"

	if CanAccess(Request{PersonAccess: personAccess, Action: ActionRead, Resource: resource}) {
		t.Fatal("finance non-member should not read finance circle files")
	}
}

func TestPrivateFileOnlyAllowsOwner(t *testing.T) {
	ownerAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}}
	otherAccess := policy.PersonAccess{PersonID: "person-2", Circles: []string{"member"}}
	resource := "file:private:person-1"

	if !CanAccess(Request{PersonAccess: ownerAccess, Action: ActionRead, Resource: resource}) {
		t.Fatal("private owner should read private files")
	}
	if CanAccess(Request{PersonAccess: otherAccess, Action: ActionRead, Resource: resource}) {
		t.Fatal("other person should not read private files")
	}
}

func TestRepresentativeToolPolicy(t *testing.T) {
	resourceAccessRules := []policy.ResourceAccessPolicy{{
		Resource: "tool:company_broadcast_send",
		Actions:  []string{ActionExecute},
		Circles:  []string{"representative"},
	}}
	representativeAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"member", "representative"}, ResourceAccessRules: resourceAccessRules}
	memberAccess := policy.PersonAccess{PersonID: "person-2", Circles: []string{"member"}, ResourceAccessRules: resourceAccessRules}

	if !CanAccess(Request{PersonAccess: representativeAccess, Action: ActionExecute, Resource: "tool:company_broadcast_send"}) {
		t.Fatal("representative should execute representative tool")
	}
	if CanAccess(Request{PersonAccess: memberAccess, Action: ActionExecute, Resource: "tool:company_broadcast_send"}) {
		t.Fatal("a member should not execute representative tool")
	}
}

func TestFlowResourcePolicies(t *testing.T) {
	resourceAccessRules := []policy.ResourceAccessPolicy{
		{Resource: "api:flow.summary", Actions: []string{ActionRead}, Circles: []string{"member"}},
		{Resource: "api:flow.task", Actions: []string{"create", "update"}, Circles: []string{"member"}},
		{Resource: "api:flow.definition", Actions: []string{ActionManage}, Circles: []string{"admin"}},
		{Resource: "tool:task_add", Actions: []string{ActionExecute}, Circles: []string{"member"}},
		{Resource: "tool:task_list", Actions: []string{ActionExecute}, Circles: []string{"member"}},
		{Resource: "tool:task_update", Actions: []string{ActionExecute}, Circles: []string{"member"}},
	}
	memberAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}, ResourceAccessRules: resourceAccessRules}
	adminAccess := policy.PersonAccess{PersonID: "person-2", Circles: []string{"member", "admin"}, ResourceAccessRules: resourceAccessRules}
	guestAccess := policy.PersonAccess{PersonID: "person-3", ResourceAccessRules: resourceAccessRules}

	if !CanAccess(Request{PersonAccess: memberAccess, Action: ActionRead, Resource: "api:flow.summary"}) {
		t.Fatal("a member should read Flow summary")
	}
	if !CanAccess(Request{PersonAccess: memberAccess, Action: "create", Resource: "api:flow.task"}) {
		t.Fatal("a member should create Flow task")
	}
	if CanAccess(Request{PersonAccess: memberAccess, Action: ActionManage, Resource: "api:flow.definition"}) {
		t.Fatal("a member should not manage Flow definitions")
	}
	if !CanAccess(Request{PersonAccess: adminAccess, Action: ActionManage, Resource: "api:flow.definition"}) {
		t.Fatal("admin should manage Flow definitions")
	}
	if !CanAccess(Request{PersonAccess: memberAccess, Action: ActionExecute, Resource: "tool:task_add"}) {
		t.Fatal("a member should execute Flow task add tool")
	}
	if !CanAccess(Request{PersonAccess: memberAccess, Action: ActionExecute, Resource: "tool:task_list"}) {
		t.Fatal("a member should execute Flow task list tool")
	}
	if !CanAccess(Request{PersonAccess: memberAccess, Action: ActionExecute, Resource: "tool:task_update"}) {
		t.Fatal("a member should execute Flow task update tool")
	}
	if CanAccess(Request{PersonAccess: guestAccess, Action: ActionExecute, Resource: "tool:task_add"}) {
		t.Fatal("guest should not execute Flow task add tool")
	}
	if CanAccess(Request{PersonAccess: guestAccess, Action: ActionExecute, Resource: "tool:task_list"}) {
		t.Fatal("guest should not execute Flow task list tool")
	}
	if CanAccess(Request{PersonAccess: guestAccess, Action: ActionExecute, Resource: "tool:task_update"}) {
		t.Fatal("guest should not execute Flow task update tool")
	}
}
