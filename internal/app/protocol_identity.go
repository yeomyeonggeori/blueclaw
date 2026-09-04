package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/protocolidentity"
	capabilitycatalog "github.com/yeomyeonggeori/blueclaw/protocol/generated"
)

// expectedProtocolIdentity prefers what the appliance pinned, and otherwise
// falls back to the contract this build was generated from.
func expectedProtocolIdentity(runtimeConfiguration config.RuntimeConfiguration) protocolidentity.Identity {
	if strings.TrimSpace(runtimeConfiguration.Capabilities.ProtocolVersion) != "" {
		return protocolidentity.Identity{
			ProtocolVersion:       runtimeConfiguration.Capabilities.ProtocolVersion,
			AggregateProtocolHash: runtimeConfiguration.Capabilities.AggregateProtocolHash,
		}
	}
	builtIdentity := capabilitycatalog.BuiltProtocolIdentity()
	return protocolidentity.Identity{
		ProtocolVersion:       builtIdentity.ProtocolVersion,
		AggregateProtocolHash: builtIdentity.AggregateProtocolHash,
	}
}

func (application *Application) checkProtocolIdentity() error {
	application.protocolIdentityCheckOnce.Do(func() {
		result := application.protocolIdentityChecker.Check(context.Background(), application.protocolIdentityExpected)
		*application.protocolIdentityStatus = result
		if !result.Passed {
			application.protocolIdentityCheckError = fmt.Errorf("protocol identity check failed: %s", strings.Join(result.FailureReasons, "; "))
		}
	})
	return application.protocolIdentityCheckError
}
