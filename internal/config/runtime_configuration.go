package config

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

type RuntimeConfiguration struct {
	BaseURL       string                      `json:"baseURL"`
	Capabilities  CapabilityConfiguration     `json:"capabilities"`
	AgentProfiles []AgentProfileConfiguration `json:"agentProfiles"`
	LanguageModel LanguageModelConfiguration  `json:"languageModel"`
	Firecracker   FirecrackerConfiguration    `json:"firecracker"`
	Bridge        BridgeConfiguration         `json:"bridge"`
	Database      DatabaseConfiguration       `json:"database"`
	Memory        MemoryConfiguration         `json:"memory"`
	Agent         AgentConfiguration          `json:"agent"`
	Connectors    ConnectorConfiguration      `json:"connectors"`
	Logging       LoggingConfiguration        `json:"logging"`
	MCPServers    []MCPServerConfiguration    `json:"mcpServers"`
	Terminal      TerminalConfiguration       `json:"terminal"`
	Scheduler     SchedulerConfiguration      `json:"scheduler"`
}

type CapabilityConfiguration struct {
	Endpoint              string                     `json:"endpoint"`
	Transport             string                     `json:"transport"`
	UnixSocketPath        string                     `json:"unixSocketPath"`
	TimeoutSecond         int                        `json:"timeoutSecond"`
	VSockCID              uint32                     `json:"vsockCID"`
	VSockPort             uint32                     `json:"vsockPort"`
	ProtocolVersion       string                     `json:"protocolVersion"`
	AggregateProtocolHash string                     `json:"aggregateProtocolHash"`
	ToolDescriptors       []CapabilityToolDescriptor `json:"toolDescriptors,omitempty"`
}

// IsConfigured reports whether a capability service is reachable. Without one
// Blueclaw runs standalone and the capability protocol identity is meaningless.
func (configuration CapabilityConfiguration) IsConfigured() bool {
	if strings.TrimSpace(configuration.Endpoint) != "" || strings.TrimSpace(configuration.UnixSocketPath) != "" {
		return true
	}
	return configuration.VSockCID > 0 && configuration.VSockPort > 0
}

type CapabilityToolDescriptor = capability.ToolDescriptor
type CapabilityToolResultContract = capability.ToolResultContract
type CapabilityResourceEffectContract = capability.ResourceEffectContract
type CapabilityCompletionEvidence = capability.CompletionEvidence
type CapabilityAvailability = capability.Availability
type CapabilityIdempotency = capability.Idempotency

type AgentProfileConfiguration struct {
	Name             string   `json:"name"`
	AllowedToolNames []string `json:"allowedToolNames"`
}

type MCPServerConfiguration struct {
	Name      string                 `json:"name"`
	Transport string                 `json:"transport"`
	Command   string                 `json:"command"`
	Arguments []string               `json:"arguments"`
	Endpoint  string                 `json:"endpoint"`
	Tools     []MCPToolConfiguration `json:"tools,omitempty"`
}

type MCPToolConfiguration struct {
	Name              string                 `json:"name"`
	Namespace         string                 `json:"namespace"`
	Description       string                 `json:"description"`
	InputSchema       json.RawMessage        `json:"inputSchema"`
	InputIntentSchema json.RawMessage        `json:"inputIntentSchema,omitempty"`
	OutputSchema      json.RawMessage        `json:"outputSchema"`
	ResultContract    *MCPToolResultContract `json:"resultContract"`
	Policy            *MCPToolPolicyMetadata `json:"policy"`
}

type MCPToolResultContract struct {
	Schema            json.RawMessage             `json:"schema"`
	Effects           []MCPResourceEffectContract `json:"effects,omitempty"`
	EvidenceCondition *EvidenceCondition          `json:"evidenceCondition,omitempty"`
}

type EvidenceCondition = capability.EvidenceCondition

type MCPResourceEffectContract struct {
	ObjectType     string `json:"objectType"`
	Effect         string `json:"effect"`
	ResultField    string `json:"resultField"`
	EffectIdentity string `json:"effectIdentity"`
}

type MCPToolPolicyMetadata struct {
	PrivacyClass         string `json:"privacyClass"`
	RequiresUserPresence bool   `json:"requiresUserPresence"`
	WorksOffline         bool   `json:"worksOffline"`
	ModelVisibility      string `json:"modelVisibility"`
	PolicyResource       string `json:"policyResource"`
	SideEffectClass      string `json:"sideEffectClass"`
	RequiresApproval     bool   `json:"requiresApproval"`
	CompletionMode       string `json:"completionMode"`
	Idempotency          string `json:"idempotency"`
	IdempotencyScope     string `json:"idempotencyScope"`
}

type AgentConfiguration struct {
	Intake                       AgentIntakeConfiguration `json:"intake"`
	DefaultTaskLevel             string                   `json:"defaultTaskLevel"`
	SkillTaskLevelFloor          string                   `json:"skillTaskLevelFloor,omitempty"`
	FailureRecovery              AgentFailureRecovery     `json:"failureRecovery"`
	GenerationOptions            AgentGenerationOptions   `json:"generationOptions,omitempty"`
	AdminTaskLinkBaseURL         string                   `json:"adminTaskLinkBaseURL,omitempty"`
	AllowAdminTaskDiagnostic     bool                     `json:"allowAdminTaskDiagnostic"`
	Harness                      HarnessConfiguration     `json:"harness,omitempty"`
	OptionalFileReadPathSuffixes []string                 `json:"optionalFileReadPathSuffixes,omitempty"`
}

type HarnessConfiguration struct {
	Name             string   `json:"name,omitempty"`
	AgentCommandPath string   `json:"agentCommandPath,omitempty"`
	AgentArguments   []string `json:"agentArguments,omitempty"`
	ToolCatalogURL   string   `json:"toolCatalogURL,omitempty"`
}

type AgentIntakeConfiguration struct {
	Enabled       bool   `json:"enabled"`
	Model         string `json:"model"`
	ExecutionMode string `json:"executionMode"`
}

type AgentFailureRecovery struct {
	FailureDebtFinalizationGate bool                `json:"failureDebtFinalizationGate"`
	AttemptFingerprint          string              `json:"attemptFingerprint"`
	RecoveryBudget              AgentRecoveryBudget `json:"recoveryBudget"`
}

type AgentRecoveryBudget struct {
	CorrectedRetry int `json:"correctedRetry"`
	AlternateRoute int `json:"alternateRoute"`
	AdjacentTool   int `json:"adjacentTool"`
	NoToolFallback int `json:"noToolFallback"`
}

type AgentGenerationOptions struct {
	Seed        *int64   `json:"seed,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type LanguageModelConfiguration struct {
	DefaultProvider  string                               `json:"defaultProvider"`
	FallbackProvider string                               `json:"fallbackProvider"`
	Capability       LanguageModelCapabilityConfiguration `json:"capability"`
	Direct           LanguageModelDirectConfiguration     `json:"direct"`
}

type LanguageModelDirectConfiguration struct {
	Endpoint   string `json:"endpoint"`
	APIKeyPath string `json:"apiKeyPath"`
	Model      string `json:"model"`
}

type LanguageModelCapabilityConfiguration struct {
	Model               string `json:"model"`
	MaximumModelTier    string `json:"maximumModelTier,omitempty"`
	MinimumModelTier    string `json:"minimumModelTier,omitempty"`
	MaxModel            string `json:"maxModel"`
	XHighModel          string `json:"xhighModel"`
	HighModel           string `json:"highModel"`
	MediumModel         string `json:"mediumModel"`
	LowModel            string `json:"lowModel"`
	XLowModel           string `json:"xlowModel"`
	ExecutionMode       string `json:"executionMode"`
	ContextWindowTokens int    `json:"contextWindowTokens"`
}

type FirecrackerConfiguration struct {
	VirtualMachineMonitor  string                            `json:"virtualMachineMonitor"`
	FirecrackerPath        string                            `json:"firecrackerPath"`
	JailerPath             string                            `json:"jailerPath"`
	CloudHypervisorPath    string                            `json:"cloudHypervisorPath"`
	VfkitPath              string                            `json:"vfkitPath"`
	VirtiofsdPath          string                            `json:"virtiofsdPath"`
	DeliveryDirectoryPath  string                            `json:"deliveryDirectoryPath"`
	KernelImagePath        string                            `json:"kernelImagePath"`
	RootfsImagePath        string                            `json:"rootfsImagePath"`
	WorkspaceImagePath     string                            `json:"workspaceImagePath"`
	WorkspaceMinimumBytes  int64                             `json:"workspaceMinimumBytes"`
	HostWorkspacePath      string                            `json:"hostWorkspacePath"`
	VCPUCount              int                               `json:"vcpuCount"`
	MemoryMiB              int                               `json:"memoryMiB"`
	VSockCID               uint32                            `json:"vsockCID"`
	HealthPortOrService    string                            `json:"healthPortOrService"`
	GuestHTTPPortOrService string                            `json:"guestHTTPPortOrService"`
	HostHTTPListenAddress  string                            `json:"hostHTTPListenAddress"`
	LogDirectoryPath       string                            `json:"logDirectoryPath"`
	RuntimeDirectoryPath   string                            `json:"runtimeDirectoryPath"`
	OutboundNetwork        OutboundNetworkConfiguration      `json:"outboundNetwork"`
	GuestListenerProxies   []GuestListenerProxyConfiguration `json:"guestListenerProxies"`
}

type OutboundNetworkConfiguration struct {
	Enabled          bool   `json:"enabled"`
	HostDeviceName   string `json:"hostDeviceName"`
	GuestMACAddress  string `json:"guestMACAddress"`
	NetworkCIDR      string `json:"networkCIDR"`
	HostAddressCIDR  string `json:"hostAddressCIDR"`
	GuestAddressCIDR string `json:"guestAddressCIDR"`
	GuestGateway     string `json:"guestGateway"`
}

type GuestListenerProxyConfiguration struct {
	GuestPort            uint32 `json:"guestPort"`
	TargetUnixSocketPath string `json:"targetUnixSocketPath"`
}

type BridgeConfiguration struct {
	Mode                     string `json:"mode"`
	AuthMode                 string `json:"authMode"`
	AuthorizedPublicKeysPath string `json:"authorizedPublicKeysPath"`
	ListenAddress            string `json:"listenAddress"`
}

type DatabaseConfiguration struct {
	Driver                 string `json:"driver"`
	ConnectionString       string `json:"connectionString"`
	MigrationDirectoryPath string `json:"migrationDirectoryPath"`
}

type MemoryConfiguration struct {
	WorkspaceID                                 string `json:"workspaceID"`
	GraphitiEndpoint                            string `json:"graphitiEndpoint"`
	GraphitiKuzuPath                            string `json:"graphitiKuzuPath"`
	PinnedMemoryRootPath                        string `json:"pinnedMemoryRootPath"`
	PinnedMemoryCharacterLimit                  int    `json:"pinnedMemoryCharacterLimit"`
	PinnedMemoryHardLimitCharacterCount         int    `json:"pinnedMemoryHardLimitCharacterCount"`
	PinnedMemoryCompressionTargetCharacterCount int    `json:"pinnedMemoryCompressionTargetCharacterCount"`
	TimeoutSecond                               int    `json:"timeoutSecond"`
}

type ConnectorConfiguration struct {
	Mattermost MattermostConnectorConfiguration `json:"mattermost"`
	Slack      SlackConnectorConfiguration      `json:"slack"`
	Signal     SignalConnectorConfiguration     `json:"signal"`
	Chatd      ChatdConnectorConfiguration      `json:"chatd"`
}

type MattermostConnectorConfiguration struct {
	BaseURL string `json:"baseURL"`
}

type SlackConnectorConfiguration struct {
	BaseURL string `json:"baseURL"`
}

type SignalConnectorConfiguration struct {
	Enabled bool `json:"enabled"`
}

type ChatdConnectorConfiguration struct {
	Endpoint         string   `json:"endpoint,omitempty"`
	TimeoutSecond    int      `json:"timeoutSecond,omitempty"`
	EnabledPlatforms []string `json:"enabledPlatforms,omitempty"`
}

type LoggingConfiguration struct {
	DirectoryPath string `json:"directoryPath"`
	RetentionDays int    `json:"retentionDays"`
}

type TerminalConfiguration struct {
	Mode                  string `json:"mode"`
	SandboxProvider       string `json:"sandboxProvider"`
	WorkspaceRootPath     string `json:"workspaceRootPath"`
	POSIXHelperPath       string `json:"posixHelperPath"`
	TimeoutSecond         int    `json:"timeoutSecond"`
	OutputMaxBytes        int    `json:"outputMaxBytes"`
	SessionMaxCount       int    `json:"sessionMaxCount"`
	AllowNetwork          bool   `json:"allowNetwork"`
	AllowInteractiveShell bool   `json:"allowInteractiveShell"`
}

type SchedulerConfiguration struct {
	RetentionCheckIntervalMinute   int `json:"retentionCheckIntervalMinute"`
	TaskSchedulePollIntervalSecond int `json:"taskSchedulePollIntervalSecond"`
	TaskRetentionDays              int `json:"taskRetentionDays"`
}

func LoadRuntimeConfiguration(path string) (RuntimeConfiguration, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return RuntimeConfiguration{}, errorValue
	}

	var configuration RuntimeConfiguration
	errorValue = json.Unmarshal(document, &configuration)
	if errorValue != nil {
		return RuntimeConfiguration{}, errorValue
	}
	if errorValue := validateCapabilityProtocolIdentity(&configuration.Capabilities); errorValue != nil {
		return RuntimeConfiguration{}, errorValue
	}

	return configuration, nil
}

func validateCapabilityProtocolIdentity(configuration *CapabilityConfiguration) error {
	if !configuration.IsConfigured() {
		return nil
	}
	if configuration.ProtocolVersion == "" || configuration.ProtocolVersion != strings.TrimSpace(configuration.ProtocolVersion) {
		return fmt.Errorf("capabilities.protocolVersion must be a non-empty trimmed string")
	}
	if configuration.AggregateProtocolHash != strings.TrimSpace(configuration.AggregateProtocolHash) {
		return fmt.Errorf("capabilities.aggregateProtocolHash must be a 64-character lowercase hexadecimal hash")
	}
	if len(configuration.AggregateProtocolHash) != 64 {
		return fmt.Errorf("capabilities.aggregateProtocolHash must be a 64-character lowercase hexadecimal hash")
	}
	if _, errorValue := hex.DecodeString(configuration.AggregateProtocolHash); errorValue != nil || strings.ToLower(configuration.AggregateProtocolHash) != configuration.AggregateProtocolHash {
		return fmt.Errorf("capabilities.aggregateProtocolHash must be a 64-character lowercase hexadecimal hash")
	}
	return nil
}
