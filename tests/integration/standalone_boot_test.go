package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/app"
	"github.com/yeomyeonggeori/blueclaw/internal/bluecollarharness"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

// Proves the claim the standalone quickstart makes: with a database and no
// capability service at all, Blueclaw builds its runtime, applies its schema,
// and passes the protocol identity check. Connector workers only start under
// Start(), so this asserts the parts that capability independence rests on.
// Needs Postgres, so it skips unless one is offered.
func TestStandaloneRuntimeIsHealthyWithoutACapabilityService(t *testing.T) {
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("set BLUECLAW_TEST_POSTGRES_URL to run the standalone boot check")
	}

	runtimeConfiguration := loadStandaloneRuntimeConfiguration(t, connectionString)
	application := app.NewApplication(runtimeConfiguration, "../../config/policy.example.json", bluecollarharness.New, app.InboundOptions{})
	healthDocument := map[string]any{}
	responseRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(responseRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/health", nil))
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &healthDocument); errorValue != nil {
		t.Fatalf("expected a health document: %v", errorValue)
	}
	database, _ := healthDocument["database"].(map[string]any)
	if database["reachable"] != true || database["migrationsApplied"] != true || database["schemaValid"] != true {
		t.Fatalf("expected the standalone schema to be applied, got %s", responseRecorder.Body.String())
	}
	protocolIdentity, _ := healthDocument["protocolIdentity"].(map[string]any)
	capabilityd, _ := protocolIdentity["capabilityd"].(map[string]any)
	if capabilityd["status"] != "not_configured" {
		t.Fatalf("expected the capability service to be reported as absent, got %s", responseRecorder.Body.String())
	}
	if protocolIdentity["passed"] != true {
		t.Fatalf("expected the protocol identity check to pass without a capability service, got %s", responseRecorder.Body.String())
	}
}

func loadStandaloneRuntimeConfiguration(t *testing.T, connectionString string) config.RuntimeConfiguration {
	t.Helper()
	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration("../../config/runtime.standalone.example.json")
	if errorValue != nil {
		t.Fatalf("expected the standalone example configuration to load: %v", errorValue)
	}
	if runtimeConfiguration.Capabilities.IsConfigured() {
		t.Fatal("expected the standalone example configuration to name no capability service")
	}
	workspaceRootPath := t.TempDir()
	runtimeConfiguration.Database.ConnectionString = connectionString
	runtimeConfiguration.Database.MigrationDirectoryPath = "../../migrations"
	runtimeConfiguration.Terminal.WorkspaceRootPath = workspaceRootPath
	runtimeConfiguration.Logging.DirectoryPath = filepath.Join(workspaceRootPath, "logs")
	return runtimeConfiguration
}
