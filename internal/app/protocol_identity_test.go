package app

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/protocolidentity"
)

func TestExpectedProtocolIdentityPinsWhatTheRuntimeDocumentStamped(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{Capabilities: config.CapabilityConfiguration{
		Endpoint:              "http://internkim-capability",
		ProtocolVersion:       "0.4.0",
		AggregateProtocolHash: "58ff1977989bacbf2db3fdce08fd57c9b52f344ca747a3322f4e60bdf6052a78",
	}}

	expected := expectedProtocolIdentity(runtimeConfiguration)

	if expected.ProtocolVersion != "0.4.0" || expected.AggregateProtocolHash == "" {
		t.Fatalf("expected the stamped identity, got %+v", expected)
	}
}

func TestExpectedProtocolIdentityPinsNothingWhenTheRuntimeDocumentStampedNothing(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{Capabilities: config.CapabilityConfiguration{
		Endpoint: "http://internkim-capability",
	}}

	expected := expectedProtocolIdentity(runtimeConfiguration)

	if expected != (protocolidentity.Identity{}) {
		t.Fatalf("expected nothing pinned, got %+v", expected)
	}
}
