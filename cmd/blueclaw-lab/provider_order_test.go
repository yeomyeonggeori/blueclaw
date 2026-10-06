package main

import (
	"slices"
	"testing"
)

func TestProviderOrderReadsACommaSeparatedPreference(t *testing.T) {
	if order := providerOrder(" Fireworks, CoreWeave ,,Friendli"); !slices.Equal(order, []string{"Fireworks", "CoreWeave", "Friendli"}) {
		t.Fatalf("expected the three providers in order, got %v", order)
	}
	if order := providerOrder(""); len(order) != 0 {
		t.Fatalf("expected no preference when the variable is empty, got %v", order)
	}
}
