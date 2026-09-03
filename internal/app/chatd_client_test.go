package app

import (
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
