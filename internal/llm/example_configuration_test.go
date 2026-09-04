package llm_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
)

// The committed examples are the wire shape anything writing a runtime document
// has to produce, and internkim's renderer is held to these same files. A field
// renamed here without the examples following fails here rather than on a box.
func TestTheEndpointExampleNamesEveryTier(t *testing.T) {
	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration("../../config/runtime.standalone.example.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	namedTiers := []string{}
	for tier := range runtimeConfiguration.LanguageModel.Tiers {
		namedTiers = append(namedTiers, tier)
	}
	slices.Sort(namedTiers)
	expectedTiers := slices.Sorted(slices.Values(llm.ModelTiers))
	if !slices.Equal(namedTiers, expectedTiers) {
		t.Fatalf("the example names %v, and a ladder is %v", namedTiers, expectedTiers)
	}

	if runtimeConfiguration.LanguageModel.Embedding.Model == "" {
		t.Fatal("the example names no embedding model, so an enrolled install would index nothing")
	}

	tierProviderFactory, errorValue := llm.NewTierProviderFactory(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, tier := range llm.ModelTiers {
		tierProvider, tierError := tierProviderFactory(tier)
		if tierError != nil {
			t.Fatalf("%s: %v", tier, tierError)
		}
		if tierProvider.Provider == nil || tierProvider.Reaches == "" {
			t.Fatalf("%s reached nothing the example named", tier)
		}
	}
}

func TestTheCapabilityExampleNamesEveryTier(t *testing.T) {
	// This example is a fragment of what a device is deployed with rather than a
	// document that loads on its own, so it is decoded rather than validated.
	document, errorValue := os.ReadFile("../../config/runtime.example.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var runtimeConfiguration config.RuntimeConfiguration
	if errorValue := json.Unmarshal(document, &runtimeConfiguration); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(runtimeConfiguration.LanguageModel.Tiers) != 0 {
		t.Fatal("a device reaches its model through the capability route, so the example must name no endpoint")
	}
	tierProviderFactory, errorValue := llm.NewTierProviderFactory(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, tier := range llm.ModelTiers {
		if _, tierError := tierProviderFactory(tier); tierError != nil {
			t.Fatalf("%s: %v", tier, tierError)
		}
	}
}
