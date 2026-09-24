package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRuntimeDocument(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.json")
	if errorValue := os.WriteFile(path, []byte(document), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	return path
}

func TestTheLoaderFillsInEnvironmentReferences(t *testing.T) {
	t.Setenv("BLUECLAW_MODEL", "example/model")
	t.Setenv("BLUECLAW_DATABASE_URL", `postgres://user:pa"ss@host/db`)
	path := writeRuntimeDocument(t, `{
		"languageModel": {"tiers": {"low": [{"endpoint": "https://example.com/v1", "model": "${BLUECLAW_MODEL}"}]}},
		"database": {"connectionString": "${BLUECLAW_DATABASE_URL}"},
		"baseURL": "http://127.0.0.1:8081/$literal"
	}`)
	configuration, errorValue := LoadRuntimeConfiguration(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if model := configuration.LanguageModel.Tiers["low"][0].Model; model != "example/model" {
		t.Fatalf("expected the model from the environment, got %q", model)
	}
	if connection := configuration.Database.ConnectionString; connection != `postgres://user:pa"ss@host/db` {
		t.Fatalf("expected a value with a quote to arrive intact, got %q", connection)
	}
	if configuration.BaseURL != "http://127.0.0.1:8081/$literal" {
		t.Fatalf("a dollar sign without braces is not a reference, got %q", configuration.BaseURL)
	}
}

func TestTheLoaderNamesEveryUnsetReference(t *testing.T) {
	t.Setenv("BLUECLAW_EMPTY", "")
	path := writeRuntimeDocument(t, `{"languageModel": {"tiers": {"low": [{"endpoint": "${BLUECLAW_UNSET_ENDPOINT}", "model": "${BLUECLAW_EMPTY}"}]}}}`)
	_, errorValue := LoadRuntimeConfiguration(path)
	if errorValue == nil || !strings.Contains(errorValue.Error(), "BLUECLAW_EMPTY, BLUECLAW_UNSET_ENDPOINT") {
		t.Fatalf("expected both unset variables named, got %v", errorValue)
	}
}
