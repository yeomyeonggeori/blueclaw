package main

import (
	"slices"
	"testing"
)

func TestACircleGroupNobodyDeclaresOrUsesIsRetired(t *testing.T) {
	retired := retiredCircleGroups(
		[]string{"bc_circle_member", "bc_circle_c-level", "bc_circle_hr", "bc_circle_leadership", "bc_shared", "staff"},
		[]string{"bc_circle_member", "bc_circle_leadership", "bc_shared"},
		[]string{"member", "leadership", "hr"},
	)

	if !slices.Equal(retired, []string{"bc_circle_c-level"}) {
		t.Fatalf("retired %v; only a circle group neither declared nor backing a folder may go", retired)
	}
}

func TestOnlyCircleGroupsAreEverRetired(t *testing.T) {
	retired := retiredCircleGroups([]string{"bc_shared", "bc_person_one", "blueclaw", "wheel"}, nil, nil)

	if len(retired) != 0 {
		t.Fatalf("retired %v; groups that are not circle groups are never touched", retired)
	}
}
