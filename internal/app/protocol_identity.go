package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/protocolidentity"
)

type protocolIdentityComponents struct {
	expected protocolidentity.Identity
	status   *protocolidentity.Result
	checker  protocolidentity.Checker
}

func newProtocolIdentity(runtimeConfiguration config.RuntimeConfiguration, capabilityClient capability.Client) protocolIdentityComponents {
	expected := expectedProtocolIdentity(runtimeConfiguration)
	return protocolIdentityComponents{
		expected: expected,
		status:   &protocolidentity.Result{Expected: expected},
		checker: protocolidentity.NewChecker(protocolidentity.Configuration{
			CapabilityEndpoint:   runtimeConfiguration.Capabilities.Endpoint,
			Timeout:              time.Duration(runtimeConfiguration.Capabilities.TimeoutSecond) * time.Second,
			CapabilityHTTPClient: capabilityClient.HTTPClient,
		}),
	}
}

func expectedProtocolIdentity(runtimeConfiguration config.RuntimeConfiguration) protocolidentity.Identity {
	if strings.TrimSpace(runtimeConfiguration.Capabilities.ProtocolVersion) == "" {
		return protocolidentity.Identity{}
	}
	return protocolidentity.Identity{
		ProtocolVersion:       runtimeConfiguration.Capabilities.ProtocolVersion,
		AggregateProtocolHash: runtimeConfiguration.Capabilities.AggregateProtocolHash,
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
