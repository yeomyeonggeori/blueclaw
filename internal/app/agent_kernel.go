package app

import (
	"os"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

func newCapabilityClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capability.Configuration{
		Endpoint:       runtimeConfiguration.Capabilities.Endpoint,
		Transport:      runtimeConfiguration.Capabilities.Transport,
		UnixSocketPath: runtimeConfiguration.Capabilities.UnixSocketPath,
		VSockCID:       runtimeConfiguration.Capabilities.VSockCID,
		VSockPort:      runtimeConfiguration.Capabilities.VSockPort,
		Timeout:        time.Duration(runtimeConfiguration.Capabilities.TimeoutSecond) * time.Second,
	})
}

func currentExecutablePath() string {
	executablePath, errorValue := os.Executable()
	if errorValue != nil {
		return ""
	}
	return executablePath
}
