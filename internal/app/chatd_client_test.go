package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

func TestNewChatdClientDefaultsEndpointWhenUnconfigured(t *testing.T) {
	client := newChatdClient(config.RuntimeConfiguration{})

	if client.Endpoint != connectors.DefaultChatdEndpoint {
		t.Fatalf("expected default chatd endpoint, got %q", client.Endpoint)
	}
}

func TestNewChatdClientUsesConfiguredEndpoint(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{
		Connectors: config.ConnectorConfiguration{
			Chatd: config.ChatdConnectorConfiguration{Endpoint: "http://chatd.example:19090"},
		},
	}

	client := newChatdClient(runtimeConfiguration)

	if client.Endpoint != "http://chatd.example:19090" {
		t.Fatalf("expected configured chatd endpoint, got %q", client.Endpoint)
	}
}

func TestTheChatdClientCarriesTheServiceTokenItIsPointedAt(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "service-token")
	if errorValue := os.WriteFile(tokenPath, []byte(strings.Repeat("a", 64)+"\n"), 0o440); errorValue != nil {
		t.Fatal(errorValue)
	}
	authorizations := []string{}
	chatd := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		_, _ = responseWriter.Write([]byte(`{}`))
	}))
	defer chatd.Close()
	client := newChatdClient(config.RuntimeConfiguration{ServiceTokenPath: tokenPath, Connectors: config.ConnectorConfiguration{
		Chatd: config.ChatdConnectorConfiguration{Endpoint: chatd.URL},
	}})

	if errorValue := client.PostJSON(context.Background(), "/v1/platform/buzz/reply.send", map[string]string{}, nil); errorValue != nil {
		t.Fatal(errorValue)
	}

	if len(authorizations) != 1 || authorizations[0] != "Bearer "+strings.Repeat("a", 64) {
		t.Fatalf("chatd was asked with %q", authorizations)
	}
}

func TestTheChatdClientSendsNothingWhenItsServiceTokenCannotBeRead(t *testing.T) {
	reached := false
	chatd := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer chatd.Close()
	client := newChatdClient(config.RuntimeConfiguration{ServiceTokenPath: filepath.Join(t.TempDir(), "missing"), Connectors: config.ConnectorConfiguration{
		Chatd: config.ChatdConnectorConfiguration{Endpoint: chatd.URL},
	}})

	errorValue := client.PostJSON(context.Background(), "/v1/platform/buzz/reply.send", map[string]string{}, nil)

	if errorValue == nil || reached {
		t.Fatalf("a call with no readable token reached chatd: %v", errorValue)
	}
}
