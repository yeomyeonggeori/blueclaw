package app

import (
	"net/http"
	"net/http/httptest"
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

	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)

	registered := application.connectorRuntime.Health().RegisteredPlatforms
	slices.Sort(registered)
	declared := capabilitycatalog.ConnectorPlatformNames()
	slices.Sort(declared)
	if !slices.Equal(registered, declared) {
		t.Fatalf("registered platforms drifted from the protocol: registered=%v declared=%v", registered, declared)
	}
}

func TestNewApplicationRegistersAChatdPlatformTheProtocolDoesNotName(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{
		Connectors: config.ConnectorConfiguration{
			Chatd: config.ChatdConnectorConfiguration{EnabledPlatforms: []string{"a-messenger-on-its-way-out"}},
		},
	}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()

	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)

	registered := application.connectorRuntime.Health().RegisteredPlatforms
	if !slices.Contains(registered, "a-messenger-on-its-way-out") {
		t.Fatalf("expected a configured chatd platform to reach the runtime, got %v", registered)
	}
}

func TestConnectorEventRouteAnswersEveryDeclaredPlatform(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()

	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)

	for _, platform := range capabilitycatalog.ConnectorPlatformNames() {
		request := httptest.NewRequest(http.MethodPost, "/connectors/"+platform+"/events", strings.NewReader("{}"))
		recorder := httptest.NewRecorder()
		application.httpServer.Handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusNotFound {
			t.Fatalf("expected an ingress route for the declared platform %q", platform)
		}
	}
}
