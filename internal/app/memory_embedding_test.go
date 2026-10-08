package app

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
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
	if runtimeConfiguration.Memory.EmbeddingModel != llm.DefaultEmbeddingModelName {
		t.Fatalf("expected the example to name %q, got %q", llm.DefaultEmbeddingModelName, runtimeConfiguration.Memory.EmbeddingModel)
	}
	if runtimeConfiguration.Memory.EmbeddingDimensions != llm.DefaultEmbeddingDimensions {
		t.Fatalf("expected the example to ask for %d dimensions, got %d", llm.DefaultEmbeddingDimensions, runtimeConfiguration.Memory.EmbeddingDimensions)
	}
}

func TestConfiguredEmbeddingDimensionsWinOverTheDefault(t *testing.T) {
	configured := config.RuntimeConfiguration{Memory: config.MemoryConfiguration{EmbeddingDimensions: 512}}
	if width := llm.ConfiguredEmbeddingDimensions(configured); width != 512 {
		t.Fatalf("expected a configured width to be used, got %d", width)
	}
	if width := llm.ConfiguredEmbeddingDimensions(config.RuntimeConfiguration{}); width != llm.DefaultEmbeddingDimensions {
		t.Fatalf("expected the default width when none is configured, got %d", width)
	}
}

// The store reads the model name off the embedder, so the name it records and
// the name the embedder asks for cannot be two different strings.
func TestTheEmbedderNamesTheModelItWasConfiguredWith(t *testing.T) {
	client := llm.CapabilityEmbeddingClient{ModelName: "some-other/embedder"}
	if name := client.EmbeddingModelName(); name != "some-other/embedder" {
		t.Fatalf("expected the embedder to name its configured model, got %q", name)
	}
}
