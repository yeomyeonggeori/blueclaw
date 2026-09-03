package app

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

func TestNewPlatformAdapterDefaultsToCapability(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	capabilityClient := capability.Client{Endpoint: "http://capability.test"}
	chatdClient := capability.Client{Endpoint: "http://chatd.test"}

	adapter := newPlatformAdapter("slack", runtimeConfiguration, capabilityClient, chatdClient)

	capabilityAdapter, isCapabilityAdapter := adapter.(connectors.CapabilityPlatformAdapter)
	if !isCapabilityAdapter {
		t.Fatalf("expected capability adapter by default, got %T", adapter)
	}
	if capabilityAdapter.CapabilityClient.Endpoint != "http://capability.test" {
		t.Fatalf("expected capability client to carry configured endpoint, got %q", capabilityAdapter.CapabilityClient.Endpoint)
	}
}

func TestNewPlatformAdapterSwitchesToChatdWhenPlatformEnabled(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{
		Connectors: config.ConnectorConfiguration{
			Chatd: config.ChatdConnectorConfiguration{EnabledPlatforms: []string{"Buzz"}},
		},
	}
	capabilityClient := capability.Client{Endpoint: "http://capability.test"}
	chatdClient := capability.Client{Endpoint: "http://chatd.test"}

	buzzAdapter := newPlatformAdapter("buzz", runtimeConfiguration, capabilityClient, chatdClient)
	slackAdapter := newPlatformAdapter("slack", runtimeConfiguration, capabilityClient, chatdClient)

	chatdAdapter, isChatdAdapter := buzzAdapter.(connectors.ChatdPlatformAdapter)
	if !isChatdAdapter {
		t.Fatalf("expected chatd adapter once buzz is enabled, got %T", buzzAdapter)
	}
	if chatdAdapter.ChatdClient.Endpoint != "http://chatd.test" {
		t.Fatalf("expected chatd client to carry configured endpoint, got %q", chatdAdapter.ChatdClient.Endpoint)
	}

	if _, isCapabilityAdapter := slackAdapter.(connectors.CapabilityPlatformAdapter); !isCapabilityAdapter {
		t.Fatalf("expected slack to stay on capabilityd when not listed, got %T", slackAdapter)
	}
}

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
