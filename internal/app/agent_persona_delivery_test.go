package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/httpserver"
)

func TestDeliveredPersonaReachesIdentityAndInstructionReaders(t *testing.T) {
	root := t.TempDir()
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Terminal.WorkspaceRootPath = root
	if identity := loadAgentIdentity(runtimeConfiguration); identity.Name != "" {
		t.Fatalf("unexpected initial identity: %+v", identity)
	}
	handler := httpserver.PersonaHandler{WorkspaceRootPath: root}
	response := httptest.NewRecorder()
	handler.HandleWriteAgent(response, httptest.NewRequest(http.MethodPut, "/admin/api/persona/agent", strings.NewReader(`{"identity":{"schemaVersion":1,"names":["샘플봇"],"handle":"samplebot"},"soul":{"schemaVersion":1,"values":["Verify outcomes."]}}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("delivery failed: %d %s", response.Code, response.Body.String())
	}
	identity := loadAgentIdentity(runtimeConfiguration)
	if identity.Name != "샘플봇" || identity.Handle != "samplebot" {
		t.Fatalf("delivered identity did not reach the runtime: %+v", identity)
	}
	bundle := loadAgentInstructionBundle(runtimeConfiguration, agentruntime.NewCapabilityRegistry(capability.Client{}, nil))
	if !strings.Contains(bundle.Prompt, "샘플봇") || !strings.Contains(bundle.Prompt, "Verify outcomes.") {
		t.Fatalf("delivered persona did not reach instructions: %s", bundle.Prompt)
	}
}
