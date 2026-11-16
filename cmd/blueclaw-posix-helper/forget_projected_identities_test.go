package main

import (
	"errors"
	"slices"
	"testing"
)

func TestEveryProjectedIdentityIsForgottenAndNothingElse(t *testing.T) {
	removed := []string{}
	errorValue := forgetProjected(
		func() ([]systemIdentity, error) {
			return []systemIdentity{
				{name: "root"}, {name: "admin"}, {name: "blueclaw"}, {name: "bc_person_one"},
				{name: "bc_circle_member"}, {name: "bc_shared"}, {name: "bc"}, {name: "internkim-admin"},
			}, nil
		},
		func(name string) error {
			removed = append(removed, name)
			return nil
		},
	)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !slices.Equal(removed, []string{"bc_person_one", "bc_circle_member", "bc_shared"}) {
		t.Fatalf("removed %v; only the names the helper itself projects may go", removed)
	}
}

func TestARemovalThatFailsStopsTheRest(t *testing.T) {
	refusal := errors.New("user bc_person_one is currently used by process 42")
	removed := 0
	errorValue := forgetProjected(
		func() ([]systemIdentity, error) {
			return []systemIdentity{{name: "bc_person_one"}, {name: "bc_person_two"}}, nil
		},
		func(string) error {
			removed++
			return refusal
		},
	)

	if !errors.Is(errorValue, refusal) || removed != 1 {
		t.Fatalf("got %v after %d removals; a refused removal is reported, not skipped", errorValue, removed)
	}
}

func TestOnlyRootForgetsTheProjectedIdentities(t *testing.T) {
	errorValue := runForgetProjectedIdentities(1000, nil)

	if !errors.Is(errorValue, errForgettingIdentitiesTakesRoot) {
		t.Fatalf("got %v; the agent's own user must not remove every account", errorValue)
	}
}
