package app

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessselection"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type agentKernel struct {
	instructionBundleLoader           func() agentcontract.InstructionBundle
	agentIdentityProvider             func() agentcontract.AgentIdentity
	languageModelRuntimeConfiguration config.RuntimeConfiguration
	taskTierLanguageModels            agentcontract.TaskTierLanguageModels
	capabilityClient                  capability.Client
	embeddingClient                   llm.EmbeddingProvider
	intakeLanguageModelProvider       llm.LanguageModelProvider
	terminalService                   *security.ShellService
	toolCatalog                       toolCatalogEndpoint
	harness                           agentcontract.Harness
	harnessName                       string
	skillRetriever                    agentcontract.SkillRetriever
	refreshSkillIndex                 func(context.Context)
	startupError                      error
}

func newAgentKernel(runtimeConfiguration config.RuntimeConfiguration, agentHarnessFactory harnessdriver.Factory, services taskServices, companyProvider func() agentcontract.CompanyContext, logger *slog.Logger) agentKernel {
	logger.Info("application.initializing", "stage", "agent_kernel")
	startupInstructions := loadAgentInstructions(runtimeConfiguration)
	logSkillsMissingTheirTools(logger, startupInstructions.UnavailableSkills)
	logRejectedPersonaDocuments(logger, startupInstructions.RejectedDocuments)
	kernel := agentKernel{
		instructionBundleLoader: func() agentcontract.InstructionBundle {
			return loadAgentInstructionBundle(runtimeConfiguration)
		},
		agentIdentityProvider: func() agentcontract.AgentIdentity {
			return loadAgentIdentity(runtimeConfiguration)
		},
		languageModelRuntimeConfiguration: runtimeConfiguration,
		taskTierLanguageModels:            resolveTaskTierLanguageModelProviders(runtimeConfiguration, logger),
		capabilityClient:                  newCapabilityClient(runtimeConfiguration),
	}
	logger.Info("application.initializing", "stage", "skill_retriever")
	embeddingProvider, embeddingError := llm.NewConfiguredEmbeddingProvider(runtimeConfiguration)
	if embeddingError != nil {
		logger.Error("embedding provider configuration failed", "error", embeddingError.Error())
	}
	kernel.embeddingClient = embeddingProvider
	kernel.intakeLanguageModelProvider = resolveIntakeLanguageModelProvider(runtimeConfiguration, logger)
	kernel.terminalService = security.NewShellService(runtimeConfiguration.Terminal)
	kernel.toolCatalog = newToolCatalogEndpoint(services.taskRunService, kernel.taskTierLanguageModels.High, kernel.capabilityClient)
	harnessFactory, harnessName, selectionError := selectAgentHarness(runtimeConfiguration, agentHarnessFactory, kernel, logger)
	kernel.harnessName = harnessName
	kernel.startupError = selectionError
	kernel.harness, kernel.skillRetriever = startAgentHarness(runtimeConfiguration, harnessFactory, kernel, services, companyProvider)
	kernel.refreshSkillIndex = newSkillIndexRefresher(kernel.skillRetriever, kernel.instructionBundleLoader)
	return kernel
}

func newSkillIndexRefresher(skillRetriever agentcontract.SkillRetriever, instructionBundleLoader func() agentcontract.InstructionBundle) func(context.Context) {
	return func(ctx context.Context) {
		if skillRetriever == nil {
			return
		}
		skillRetriever.Refresh(ctx, instructionBundleLoader().Skills)
	}
}

func selectAgentHarness(runtimeConfiguration config.RuntimeConfiguration, agentHarnessFactory harnessdriver.Factory, kernel agentKernel, logger *slog.Logger) (harnessdriver.Factory, string, error) {
	selectedHarnessFactory, harnessSelectionError := harnessselection.Select(runtimeConfiguration.Agent.Harness, agentHarnessFactory, harnessselection.ToolCatalogEndpoint{
		URL:               toolCatalogURL(runtimeConfiguration),
		Resolver:          kernel.toolCatalog.resolver,
		Handler:           kernel.toolCatalog.handler,
		ApprovalGate:      kernel.toolCatalog.approvalGate,
		BridgeCommandPath: currentExecutablePath(),
	}, harnessselection.SandboxProcessBoundary{
		Runner:            kernel.terminalService.WorkspaceActorFactory(),
		WorkspaceRootPath: runtimeConfiguration.Terminal.WorkspaceRootPath,
	})
	selectedHarnessName := strings.TrimSpace(runtimeConfiguration.Agent.Harness.Name)
	if selectedHarnessName == "" {
		selectedHarnessName = harnessselection.BundledHarnessName
	}
	if harnessSelectionError != nil {
		selectedHarnessName = "unavailable"
		logger.Error("application.harness.unavailable", "error", harnessSelectionError)
		selectedHarnessFactory = agentHarnessFactory
	}
	if selectedHarnessFactory == nil {
		selectedHarnessFactory = func(harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
			return nil, nil
		}
	}
	return selectedHarnessFactory, selectedHarnessName, harnessSelectionError
}

func startAgentHarness(runtimeConfiguration config.RuntimeConfiguration, harnessFactory harnessdriver.Factory, kernel agentKernel, services taskServices, companyProvider func() agentcontract.CompanyContext) (agentcontract.Harness, agentcontract.SkillRetriever) {
	return harnessFactory(harnessdriver.Dependencies{
		RuntimeConfiguration: runtimeConfiguration,
		TaskRunStore:         services.taskRunService,
		TaskStepStore:        services.taskStepService,
		TaskArtifactStore:    services.taskArtifactService,
		ToolResultSpillStore: agentruntime.NewRequesterToolResultSpillStore(kernel.terminalService.WorkspaceActorFactory(), services.taskRunService),
		ToolResultImageSource: agentruntime.NewRequesterToolResultImageSource(
			kernel.terminalService.WorkspaceActorFactory(),
			services.taskRunService,
			runtimeConfiguration.Terminal.WorkspaceRootPath,
		),
		InstructionBundleLoader:     kernel.instructionBundleLoader,
		CompanyProvider:             companyProvider,
		EmbeddingProvider:           kernel.embeddingClient,
		EmbeddingModelName:          runtimeConfiguration.LanguageModel.Embedding.Model,
		SkillIndexPath:              skillIndexPath(runtimeConfiguration),
		TaskTierLanguageModels:      kernel.taskTierLanguageModels,
		IntakeLanguageModelProvider: kernel.intakeLanguageModelProvider,
	})
}

func newCapabilityClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capabilityConfiguration(runtimeConfiguration))
}

func capabilityConfiguration(runtimeConfiguration config.RuntimeConfiguration) capability.Configuration {
	return capability.Configuration{
		Endpoint:       runtimeConfiguration.Capabilities.Endpoint,
		Transport:      runtimeConfiguration.Capabilities.Transport,
		UnixSocketPath: runtimeConfiguration.Capabilities.UnixSocketPath,
		VSockCID:       runtimeConfiguration.Capabilities.VSockCID,
		VSockPort:      runtimeConfiguration.Capabilities.VSockPort,
		Timeout:        time.Duration(runtimeConfiguration.Capabilities.TimeoutSecond) * time.Second,
	}
}

func currentExecutablePath() string {
	executablePath, errorValue := os.Executable()
	if errorValue != nil {
		return ""
	}
	return executablePath
}
