package app

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

// A store embedded at one width cannot be searched at another, so the example
// configuration and the default the code falls back to have to name the same
// model and the same width.
func TestExampleConfigurationMatchesTheDefaultMemoryEmbedding(t *testing.T) {
	document, errorValue := os.ReadFile("../../config/runtime.example.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var runtimeConfiguration config.RuntimeConfiguration
	if errorValue := json.Unmarshal(document, &runtimeConfiguration); errorValue != nil {
		t.Fatal(errorValue)
	}
	if runtimeConfiguration.Memory.EmbeddingModel != defaultMemoryEmbedding.ModelName {
		t.Fatalf("expected the example to name %q, got %q", defaultMemoryEmbedding.ModelName, runtimeConfiguration.Memory.EmbeddingModel)
	}
	if runtimeConfiguration.Memory.EmbeddingDimensions != defaultMemoryEmbedding.Dimensions {
		t.Fatalf("expected the example to ask for %d dimensions, got %d", defaultMemoryEmbedding.Dimensions, runtimeConfiguration.Memory.EmbeddingDimensions)
	}
}

func TestConfiguredEmbeddingDimensionsWinOverTheDefault(t *testing.T) {
	if width := firstPositiveInteger(768, defaultMemoryEmbedding.Dimensions); width != 768 {
		t.Fatalf("expected a configured width to be used, got %d", width)
	}
	if width := firstPositiveInteger(0, defaultMemoryEmbedding.Dimensions); width != defaultMemoryEmbedding.Dimensions {
		t.Fatalf("expected the default width when none is configured, got %d", width)
	}
}
