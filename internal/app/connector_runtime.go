package app

import (
	"slices"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	capabilitycatalog "github.com/yeomyeonggeori/blueclaw/protocol/generated"
)

func newChatdClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capability.Configuration{
		Endpoint: firstNonEmptyString(runtimeConfiguration.Connectors.Chatd.Endpoint, connectors.DefaultChatdEndpoint),
		Timeout:  time.Duration(runtimeConfiguration.Connectors.Chatd.TimeoutSecond) * time.Second,
	})
}

func platformsChatdServesBeyondTheProtocol(chatdConfiguration config.ChatdConnectorConfiguration) []string {
	platforms := []string{}
	for _, enabledPlatform := range chatdConfiguration.EnabledPlatforms {
		platform := strings.ToLower(strings.TrimSpace(enabledPlatform))
		if platform == "" || capabilitycatalog.IsConnectorPlatform(platform) || slices.Contains(platforms, platform) {
			continue
		}
		platforms = append(platforms, platform)
	}
	return platforms
}
