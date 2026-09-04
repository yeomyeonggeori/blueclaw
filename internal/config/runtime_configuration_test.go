package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadRuntimeConfigurationIncludesGuestAndBridge(t *testing.T) {
	workspacePath := t.TempDir()
	runtimeConfigurationPath := filepath.Join(workspacePath, "runtime.json")
	runtimeConfigurationDocument := `{
  "baseURL": "http://127.0.0.1:8080",
  "capabilities": {
    "endpoint": "http://127.0.0.1:7781",
    "unixSocketPath": "/run/internkim/capability.sock",
    "timeoutSecond": 15,
    "protocolVersion": "0.4.0",
    "aggregateProtocolHash": "58ff1977989bacbf2db3fdce08fd57c9b52f344ca747a3322f4e60bdf6052a78",
    "toolDescriptors": [
      {
        "name": "browser_open",
        "inputSchema": {"type": "object"},
        "inputIntentSchema": {"type": "object"},
        "sideEffectClass": "browser"
      },
      {
        "name": "user_confirm",
        "requiresApproval": true
      }
    ]
  },
  "languageModel": {
    "contextWindowTokens": 1048576,
    "capability": {
      "model": "gemma-4-E4B-it",
      "executionMode": "auto"
    }
  },
  "guest": {
    "cloudHypervisorPath": "/usr/bin/cloud-hypervisor",
    "kernelImagePath": "/opt/kernel",
    "rootfsImagePath": "/opt/rootfs.ext4",
    "workspaceImagePath": "/var/lib/blueclaw/workspace.ext4",
    "vcpuCount": 4,
    "memoryMiB": 8192,
    "vsockCID": 52,
    "healthPortOrService": "8080",
    "logDirectoryPath": "/var/log/blueclaw"
  },
  "bridge": {
    "mode": "localAgent",
    "authMode": "sshKeyReuse",
    "authorizedPublicKeysPath": "/var/lib/blueclaw/authorized_companions",
    "listenAddress": "127.0.0.1:7778"
  },
  "database": {
    "driver": "postgres",
    "connectionString": "postgres://blueclaw@/blueclaw?host=/var/run/postgresql&sslmode=disable",
    "migrationDirectoryPath": "/workspace/.blueclaw/runtime/current/migrations"
  },
  "memory": {
    "workspaceID": "acme",
    "graphitiEndpoint": "http://127.0.0.1:7791",
    "graphitiKuzuPath": "/workspace/.blueclaw/graphiti/kuzu",
    "timeoutSecond": 15
  },
  "agent": {
    "intake": {
      "enabled": true,
      "executionMode": "auto"
    },
    "defaultTaskLevel": "low",
    "generationOptions": {
      "seed": 41,
      "temperature": 0
    },
    "failureRecovery": {
      "failureDebtFinalizationGate": true,
      "attemptFingerprint": "tool_input_error_code",
      "recoveryBudget": {
        "correctedRetry": 1,
        "alternateRoute": 1,
        "adjacentTool": 2,
        "noToolFallback": 1
      }
    }
  },
  "agentProfiles": [
    {
      "name": "default",
      "allowedToolNames": ["conversation_history", "memory_search", "echo"]
    }
  ],
  "mcpServers": [
    {
      "name": "echo",
      "transport": "stdio",
      "command": "/bin/echo",
      "tools": [{
        "name": "echo",
        "namespace": "test",
        "description": "Echo input",
        "inputSchema": {"type": "object"},
        "inputIntentSchema": {"type": "object"},
        "policy": {
          "privacyClass": "test",
          "modelVisibility": "visible",
          "policyResource": "tool:test.echo",
          "sideEffectClass": "read",
          "completionMode": "none",
          "idempotency": "supported"
        }
      }]
    }
  ],
  "connectors": {
    "chatd": {
      "endpoint": "http://127.0.0.1:8090",
      "enabledPlatforms": ["buzz"]
    }
  },
  "terminal": {
    "mode": "native"
  },
  "logging": {
    "directoryPath": "/workspace/.blueclaw/logs",
    "retentionDays": 7
  },
  "scheduler": {
    "retentionCheckIntervalMinute": 60,
    "taskSchedulePollIntervalSecond": 30
  }
}`

	errorValue := os.WriteFile(runtimeConfigurationPath, []byte(runtimeConfigurationDocument), 0o600)
	if errorValue != nil {
		t.Fatalf("expected runtime configuration to be written: %v", errorValue)
	}

	runtimeConfiguration, errorValue := LoadRuntimeConfiguration(runtimeConfigurationPath)
	if errorValue != nil {
		t.Fatalf("expected runtime configuration to load: %v", errorValue)
	}

	if runtimeConfiguration.Guest.VCPUCount != 4 {
		t.Fatalf("expected vcpu count to match, got %d", runtimeConfiguration.Guest.VCPUCount)
	}
	if runtimeConfiguration.Guest.VSockCID != 52 {
		t.Fatalf("expected vsock cid to match, got %d", runtimeConfiguration.Guest.VSockCID)
	}
	if runtimeConfiguration.Bridge.AuthMode != "sshKeyReuse" {
		t.Fatalf("expected bridge auth mode to match, got %q", runtimeConfiguration.Bridge.AuthMode)
	}
	if runtimeConfiguration.Bridge.ListenAddress != "127.0.0.1:7778" {
		t.Fatalf("expected bridge listen address to match, got %q", runtimeConfiguration.Bridge.ListenAddress)
	}
	if runtimeConfiguration.Capabilities.Endpoint != "http://127.0.0.1:7781" {
		t.Fatalf("expected capability endpoint to match, got %q", runtimeConfiguration.Capabilities.Endpoint)
	}
	if runtimeConfiguration.Capabilities.ProtocolVersion != "0.4.0" {
		t.Fatalf("expected protocol version to match, got %q", runtimeConfiguration.Capabilities.ProtocolVersion)
	}
	if runtimeConfiguration.Capabilities.AggregateProtocolHash != "58ff1977989bacbf2db3fdce08fd57c9b52f344ca747a3322f4e60bdf6052a78" {
		t.Fatalf("expected aggregate protocol hash to match, got %q", runtimeConfiguration.Capabilities.AggregateProtocolHash)
	}
	if len(runtimeConfiguration.Capabilities.ToolDescriptors) != 2 {
		t.Fatalf("expected capability descriptors to load, got %+v", runtimeConfiguration.Capabilities.ToolDescriptors)
	}
	if string(runtimeConfiguration.Capabilities.ToolDescriptors[0].InputSchema) != `{"type": "object"}` && string(runtimeConfiguration.Capabilities.ToolDescriptors[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("expected descriptor input schema to load, got %s", runtimeConfiguration.Capabilities.ToolDescriptors[0].InputSchema)
	}
	if len(runtimeConfiguration.Capabilities.ToolDescriptors[0].InputIntentSchema) == 0 {
		t.Fatal("expected descriptor input intent schema to load")
	}
	if !runtimeConfiguration.Capabilities.ToolDescriptors[1].RequiresApproval {
		t.Fatalf("expected descriptor approval flag to load, got %+v", runtimeConfiguration.Capabilities.ToolDescriptors[1])
	}
	if runtimeConfiguration.Capabilities.UnixSocketPath != "/run/internkim/capability.sock" {
		t.Fatalf("expected capability unix socket path to match, got %q", runtimeConfiguration.Capabilities.UnixSocketPath)
	}
	if runtimeConfiguration.Capabilities.TimeoutSecond != 15 {
		t.Fatalf("expected capability timeout to match, got %d", runtimeConfiguration.Capabilities.TimeoutSecond)
	}
	if runtimeConfiguration.Connectors.Chatd.Endpoint != "http://127.0.0.1:8090" {
		t.Fatalf("expected chatd endpoint to match, got %q", runtimeConfiguration.Connectors.Chatd.Endpoint)
	}
	if !slices.Equal(runtimeConfiguration.Connectors.Chatd.EnabledPlatforms, []string{"buzz"}) {
		t.Fatalf("expected chatd to name the enabled platforms, got %v", runtimeConfiguration.Connectors.Chatd.EnabledPlatforms)
	}
	if runtimeConfiguration.LanguageModel.Capability.Model != "gemma-4-E4B-it" {
		t.Fatalf("expected capability model to match, got %q", runtimeConfiguration.LanguageModel.Capability.Model)
	}
	if runtimeConfiguration.LanguageModel.Capability.ExecutionMode != "auto" {
		t.Fatalf("expected capability execution mode to match, got %q", runtimeConfiguration.LanguageModel.Capability.ExecutionMode)
	}
	if runtimeConfiguration.LanguageModel.ContextWindowTokens != 1048576 {
		t.Fatalf("expected the context window to match, got %d", runtimeConfiguration.LanguageModel.ContextWindowTokens)
	}
	if runtimeConfiguration.Database.Driver != "postgres" {
		t.Fatalf("expected database driver to match, got %q", runtimeConfiguration.Database.Driver)
	}
	if runtimeConfiguration.Database.MigrationDirectoryPath != "/workspace/.blueclaw/runtime/current/migrations" {
		t.Fatalf("expected migration directory to match, got %q", runtimeConfiguration.Database.MigrationDirectoryPath)
	}
	if runtimeConfiguration.Memory.WorkspaceID != "acme" {
		t.Fatalf("expected memory workspace id to match, got %q", runtimeConfiguration.Memory.WorkspaceID)
	}
	if runtimeConfiguration.Memory.GraphitiEndpoint != "http://127.0.0.1:7791" {
		t.Fatalf("expected graphiti endpoint to match, got %q", runtimeConfiguration.Memory.GraphitiEndpoint)
	}
	if runtimeConfiguration.Memory.GraphitiKuzuPath != "/workspace/.blueclaw/graphiti/kuzu" {
		t.Fatalf("expected graphiti kuzu path to match, got %q", runtimeConfiguration.Memory.GraphitiKuzuPath)
	}
	if !runtimeConfiguration.Agent.Intake.Enabled {
		t.Fatal("expected agent intake to be enabled")
	}
	if runtimeConfiguration.Agent.Intake.ExecutionMode != "auto" {
		t.Fatalf("expected agent intake execution mode to match, got %q", runtimeConfiguration.Agent.Intake.ExecutionMode)
	}
	if runtimeConfiguration.Agent.DefaultTaskLevel != "low" {
		t.Fatalf("expected agent default task level to match, got %q", runtimeConfiguration.Agent.DefaultTaskLevel)
	}
	if runtimeConfiguration.Agent.GenerationOptions.Seed == nil || *runtimeConfiguration.Agent.GenerationOptions.Seed != 41 {
		t.Fatalf("expected agent generation seed to load, got %+v", runtimeConfiguration.Agent.GenerationOptions)
	}
	if runtimeConfiguration.Agent.GenerationOptions.Temperature == nil || *runtimeConfiguration.Agent.GenerationOptions.Temperature != 0 {
		t.Fatalf("expected agent generation temperature to load, got %+v", runtimeConfiguration.Agent.GenerationOptions)
	}
	if !runtimeConfiguration.Agent.FailureRecovery.FailureDebtFinalizationGate {
		t.Fatal("expected agent failure debt finalization gate to load")
	}
	if runtimeConfiguration.Agent.FailureRecovery.AttemptFingerprint != "tool_input_error_code" {
		t.Fatalf("expected attempt fingerprint mode to match, got %q", runtimeConfiguration.Agent.FailureRecovery.AttemptFingerprint)
	}
	if runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.CorrectedRetry != 1 || runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.AdjacentTool != 2 {
		t.Fatalf("expected recovery budget to load, got %+v", runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget)
	}
	if len(runtimeConfiguration.AgentProfiles) != 1 || runtimeConfiguration.AgentProfiles[0].AllowedToolNames[2] != "echo" {
		t.Fatalf("expected agent profile tool allowlist to load, got %+v", runtimeConfiguration.AgentProfiles)
	}
	if len(runtimeConfiguration.MCPServers) != 1 || len(runtimeConfiguration.MCPServers[0].Tools) != 1 || runtimeConfiguration.MCPServers[0].Tools[0].Name != "echo" {
		t.Fatalf("expected canonical MCP tools to load, got %+v", runtimeConfiguration.MCPServers)
	}
	if len(runtimeConfiguration.MCPServers[0].Tools[0].InputIntentSchema) == 0 {
		t.Fatal("expected MCP input intent schema to load")
	}
	if runtimeConfiguration.Logging.RetentionDays != 7 {
		t.Fatalf("expected log retention to match, got %d", runtimeConfiguration.Logging.RetentionDays)
	}
	if runtimeConfiguration.Scheduler.TaskSchedulePollIntervalSecond != 30 {
		t.Fatalf("expected task schedule poll interval to match, got %d", runtimeConfiguration.Scheduler.TaskSchedulePollIntervalSecond)
	}
}

func TestLoadRuntimeConfigurationRejectsMissingOrInvalidCapabilityProtocolIdentity(t *testing.T) {
	testCases := []struct {
		name             string
		identityDocument string
		errorFragment    string
	}{
		{name: "missing version", identityDocument: `"aggregateProtocolHash":"` + strings.Repeat("a", 64) + `"`, errorFragment: "protocolVersion must be"},
		{name: "missing hash", identityDocument: `"protocolVersion":"0.4.0"`, errorFragment: "aggregateProtocolHash"},
		{name: "invalid hash", identityDocument: `"protocolVersion":"0.4.0","aggregateProtocolHash":"not-a-hash"`, errorFragment: "aggregateProtocolHash"},
		{name: "uppercase hash", identityDocument: `"protocolVersion":"0.4.0","aggregateProtocolHash":"` + strings.Repeat("A", 64) + `"`, errorFragment: "aggregateProtocolHash"},
		{name: "padded version", identityDocument: `"protocolVersion":" 0.4.0","aggregateProtocolHash":"` + strings.Repeat("a", 64) + `"`, errorFragment: "protocolVersion must be"},
		{name: "padded hash", identityDocument: `"protocolVersion":"0.4.0","aggregateProtocolHash":"` + strings.Repeat("a", 64) + ` "`, errorFragment: "aggregateProtocolHash"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runtimeConfigurationPath := filepath.Join(t.TempDir(), "runtime.json")
			document := `{"capabilities":{"endpoint":"http://internkim-capability",` + testCase.identityDocument + `}}`
			if errorValue := os.WriteFile(runtimeConfigurationPath, []byte(document), 0o600); errorValue != nil {
				t.Fatal(errorValue)
			}
			_, errorValue := LoadRuntimeConfiguration(runtimeConfigurationPath)
			if errorValue == nil || !strings.Contains(errorValue.Error(), testCase.errorFragment) {
				t.Fatalf("expected %q error, got %v", testCase.errorFragment, errorValue)
			}
		})
	}
}
