package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPersonaIsInstalledWhereTheRuntimeReadsIt(t *testing.T) {
	root := t.TempDir()
	handler := PersonaHandler{WorkspaceRootPath: root}
	for _, name := range []string{"BOT_PROFILE.yaml", "BOT_PROFILE.md", "IDENTITY.md", "SOUL.md"} {
		if errorValue := os.WriteFile(filepath.Join(root, name), []byte("retired"), 0o600); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	response := httptest.NewRecorder()
	handler.HandleWriteAgent(response, httptest.NewRequest(http.MethodPut, "/admin/api/persona/agent", strings.NewReader(`{"identity":{"schemaVersion":1,"names":["샘플봇"],"handle":"samplebot"},"soul":{"schemaVersion":1,"values":["Verify outcomes."]}}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for name, content := range map[string]string{"identity.json": "샘플봇", "soul.json": "Verify outcomes."} {
		document, errorValue := os.ReadFile(filepath.Join(root, name))
		if errorValue != nil || !strings.Contains(string(document), content) {
			t.Fatalf("runtime document %s: %s %v", name, document, errorValue)
		}
	}
	if _, errorValue := os.Stat(filepath.Join(root, "IDENTITY.md")); !os.IsNotExist(errorValue) {
		t.Fatal("legacy identity survived")
	}
}

func TestRejectedSoulDoesNotPartiallyInstallIdentity(t *testing.T) {
	root := t.TempDir()
	handler := PersonaHandler{WorkspaceRootPath: root}
	response := httptest.NewRecorder()
	handler.HandleWriteAgent(response, httptest.NewRequest(http.MethodPut, "/admin/api/persona/agent", strings.NewReader(`{"identity":{"schemaVersion":1,"names":["샘플봇"]},"soul":{"schemaVersion":1,"unknown":"ignored?"}}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
	if _, errorValue := os.Stat(filepath.Join(root, "identity.json")); !os.IsNotExist(errorValue) {
		t.Fatal("invalid bundle partially changed identity")
	}
}
