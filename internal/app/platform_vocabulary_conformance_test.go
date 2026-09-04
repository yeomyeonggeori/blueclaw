package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/bluecollarharness"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	capabilitycatalog "github.com/yeomyeonggeori/blueclaw/protocol/generated"
)

func TestNewApplicationRegistersEveryDeclaredConnectorPlatform(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()

	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New, InboundOptions{})

	registered := application.connectorRuntime.Health().RegisteredPlatforms
	slices.Sort(registered)
	declared := capabilitycatalog.ConnectorPlatformNames()
	slices.Sort(declared)
	if !slices.Equal(registered, declared) {
		t.Fatalf("registered platforms drifted from the protocol: registered=%v declared=%v", registered, declared)
	}
}

func TestNewApplicationRegistersAChatdPlatformTheProtocolDoesNotNameAndSaysSo(t *testing.T) {
	logDirectoryPath := t.TempDir()
	runtimeConfiguration := config.RuntimeConfiguration{
		Connectors: config.ConnectorConfiguration{
			Chatd: config.ChatdConnectorConfiguration{EnabledPlatforms: []string{"a-messenger-on-its-way-out"}},
		},
	}
	runtimeConfiguration.Logging.DirectoryPath = logDirectoryPath

	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New, InboundOptions{})

	registered := application.connectorRuntime.Health().RegisteredPlatforms
	if !slices.Contains(registered, "a-messenger-on-its-way-out") {
		t.Fatalf("expected a configured chatd platform to reach the runtime, got %v", registered)
	}

	log := readRuntimeLog(t, logDirectoryPath)
	if !strings.Contains(log, "connector.platform.served_beyond_the_protocol") || !strings.Contains(log, "a-messenger-on-its-way-out") {
		t.Fatalf("expected the registration to name the platform the protocol does not, got %s", log)
	}
}

func TestNewApplicationSaysNothingWhenEveryPlatformIsDeclared(t *testing.T) {
	logDirectoryPath := t.TempDir()
	runtimeConfiguration := config.RuntimeConfiguration{
		Connectors: config.ConnectorConfiguration{
			Chatd: config.ChatdConnectorConfiguration{EnabledPlatforms: capabilitycatalog.MessengerPlatformNames()},
		},
	}
	runtimeConfiguration.Logging.DirectoryPath = logDirectoryPath

	NewApplication(runtimeConfiguration, "", bluecollarharness.New, InboundOptions{})

	log := readRuntimeLog(t, logDirectoryPath)
	if !strings.Contains(log, "application.initializing") {
		t.Fatalf("expected the runtime to have written its log, got %s", log)
	}
	if strings.Contains(log, "connector.platform.served_beyond_the_protocol") {
		t.Fatalf("expected no warning when every configured platform is declared, got %s", log)
	}
}

func readRuntimeLog(t *testing.T, directoryPath string) string {
	t.Helper()
	entries, errorValue := os.ReadDir(directoryPath)
	if errorValue != nil {
		t.Fatalf("expected a runtime log directory: %v", errorValue)
	}
	log := strings.Builder{}
	for _, entry := range entries {
		document, errorValue := os.ReadFile(filepath.Join(directoryPath, entry.Name()))
		if errorValue != nil {
			t.Fatalf("expected a readable runtime log: %v", errorValue)
		}
		log.Write(document)
	}
	return log.String()
}

func TestConnectorEventRouteAnswersEveryDeclaredPlatform(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()

	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New, InboundOptions{})

	for _, platform := range capabilitycatalog.ConnectorPlatformNames() {
		request := httptest.NewRequest(http.MethodPost, "/connectors/"+platform+"/events", strings.NewReader("{}"))
		recorder := httptest.NewRecorder()
		application.httpServer.Handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusNotFound {
			t.Fatalf("expected an ingress route for the declared platform %q", platform)
		}
	}
}
