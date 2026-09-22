package app

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

func TestAProfileKeepsTheToolsItNames(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{AgentProfiles: []config.AgentProfileConfiguration{{
		Name:             "scheduling",
		AllowedToolNames: []string{"schedule_create", "schedule_cancel"},
	}}}

	allowedToolNamesByProfile := deriveAllowedToolNamesByProfile(runtimeConfiguration)
	allowedToolNames := allowedToolNamesByProfile["scheduling"]

	for _, toolName := range []string{"schedule_create", "schedule_cancel"} {
		if !containsString(allowedToolNames, toolName) {
			t.Fatalf("expected the profile to keep %s, got %+v", toolName, allowedToolNames)
		}
	}
	for _, toolName := range agentruntime.AlwaysAllowedToolNames() {
		if !containsString(allowedToolNames, toolName) {
			t.Fatalf("expected the built-in %s alongside the profile tools, got %+v", toolName, allowedToolNames)
		}
	}
}
