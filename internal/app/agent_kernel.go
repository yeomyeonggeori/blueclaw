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
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type agentKernel struct {
	instructionBundleLoader           func() agentcontract.InstructionBundle
	agentIdentityProvider             func() agentcontract.AgentIdentity
	languageModelRuntimeConfiguration config.RuntimeConfiguration
	taskTierLanguageModels            agentcontract.TaskTierLanguageModels
	capabilityClient                  capability.Client
	capabilityRegistry                *agentruntime.CapabilityRegistry
	embeddingClient                   llm.ConfiguredEmbedder
	intakeLanguageModelProvider       llm.LanguageModelProvider
	decisionModel                     model.DecisionModel
	toolSelector                      agentcontract.ToolSelector
	terminalService                   *security.ShellService
	toolCatalog                       toolCatalogEndpoint
	harness                           agentcontract.Harness
	harnessName                       string
	skillRetriever                    agentcontract.SkillRetriever
	refreshSkillIndex                 func(context.Context)
	startupError                      error
	languageModelError                error
}

type BundledToolSelectorFactory func(model.DecisionModel) agentcontract.ToolSelector

type QuestionWorderFactory func(model.LanguageModelProvider) holdrecord.QuestionWorder

func newAgentKernel(runtimeConfiguration config.RuntimeConfiguration, bundledACPFactory harnessdriver.ACPFactory, newBundledToolSelector BundledToolSelectorFactory, newQuestionWorder QuestionWorderFactory, services taskServices, companyProvider func() agentcontract.CompanyContext, logger *slog.Logger) agentKernel {
	logger.Info("application.initializing", "stage", "agent_kernel")
	capabilityClient := newCapabilityClient(runtimeConfiguration)
	capabilityRegistry := agentruntime.NewCapabilityRegistry(capabilityClient, capabilityToolDescriptors(runtimeConfiguration.Capabilities.ToolDescriptors))
	startupInstructions := loadAgentInstructions(runtimeConfiguration, capabilityRegistry)
	logSkillsThisHostCannotSatisfy(logger, startupInstructions.UnavailableSkills)
	logRejectedPersonaDocuments(logger, startupInstructions.RejectedDocuments)
	taskTierLanguageModels, taskModelsError := resolveTaskTierLanguageModelProviders(runtimeConfiguration, logger)
	intakeLanguageModel, intakeModelError := resolveIntakeLanguageModelProvider(runtimeConfiguration, logger)
	recordTasklessCall := newTasklessLLMCallRecorder(services.repositories.llmCall, logger).observer()
	taskTierLanguageModels = observedTaskTierLanguageModels(taskTierLanguageModels, recordTasklessCall)
	intakeLanguageModel = observedLanguageModel(intakeLanguageModel, recordTasklessCall)
	kernel := agentKernel{
		instructionBundleLoader: func() agentcontract.InstructionBundle {
			return loadAgentInstructionBundle(runtimeConfiguration, capabilityRegistry)
		},
		agentIdentityProvider: func() agentcontract.AgentIdentity {
			return loadAgentIdentity(runtimeConfiguration)
		},
		languageModelRuntimeConfiguration: runtimeConfiguration,
		taskTierLanguageModels:            taskTierLanguageModels,
		intakeLanguageModelProvider:       intakeLanguageModel,
		languageModelError:                firstNonNilError(taskModelsError, intakeModelError),
		capabilityClient:                  capabilityClient,
		capabilityRegistry:                capabilityRegistry,
	}
	logger.Info("application.initializing", "stage", "skill_retriever")
	embeddingProvider, embeddingError := llm.NewConfiguredEmbeddingProvider(runtimeConfiguration)
	if embeddingError != nil {
		logger.Error("embedding provider configuration failed", "error", embeddingError.Error())
	}
	kernel.embeddingClient = embeddingProvider
	kernel.decisionModel = newConfiguredDecisionModel(runtimeConfiguration, logger)
	kernel.toolSelector = newToolSelector(newBundledToolSelector, kernel.decisionModel)
	kernel.terminalService = security.NewShellService(runtimeConfiguration.Terminal)
	services.taskRunService.RegisterTaskRunTransitionObserver(task.NewTaskTemporaryDirectoryReclaimer(runtimeConfiguration.Terminal.WorkspaceRootPath, kernel.terminalService.WorkspaceActorFactory(), logger).Observe)
	kernel.toolCatalog = newToolCatalogEndpoint(services.taskRunService, newQuestionWorder, kernel.taskTierLanguageModels.High, kernel.decisionModel, kernel.capabilityClient)
	harnessFactory, harnessName, selectionError := selectAgentHarness(runtimeConfiguration, bundledACPFactory, kernel, logger)
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

func selectAgentHarness(runtimeConfiguration config.RuntimeConfiguration, bundledACPFactory harnessdriver.ACPFactory, kernel agentKernel, logger *slog.Logger) (harnessdriver.Factory, string, error) {
	selectedHarnessFactory, harnessSelectionError := harnessselection.Select(runtimeConfiguration.Agent.Harness, harnessselection.ToolCatalogEndpoint{
		URL:               toolCatalogURL(runtimeConfiguration),
		Resolver:          kernel.toolCatalog.resolver,
		Handler:           kernel.toolCatalog.handler,
		ApprovalGate:      kernel.toolCatalog.approvalGate,
		BridgeCommandPath: currentExecutablePath(),
	}, harnessselection.SandboxProcessBoundary{
		Runner:            kernel.terminalService.WorkspaceActorFactory(),
		WorkspaceRootPath: runtimeConfiguration.Terminal.WorkspaceRootPath,
	}, harnessselection.WithBundledACPFactory(bundledACPFactory))
	selectedHarnessName := strings.TrimSpace(runtimeConfiguration.Agent.Harness.Name)
	if selectedHarnessName == "" {
		selectedHarnessName = harnessselection.BundledACPHarnessName
	}
	if harnessSelectionError != nil {
		selectedHarnessName = "unavailable"
		logger.Error("application.harness.unavailable", "error", harnessSelectionError)
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
		RuntimeConfiguration:        runtimeConfiguration,
		TaskRunStore:                services.taskRunService,
		InstructionBundleLoader:     kernel.instructionBundleLoader,
		EmbeddingProvider:           kernel.embeddingClient,
		EmbeddingModelName:          runtimeConfiguration.LanguageModel.Embedding.Model,
		SkillIndexPath:              skillIndexPath(runtimeConfiguration),
		TaskTierLanguageModels:      kernel.taskTierLanguageModels,
		IntakeLanguageModelProvider: kernel.intakeLanguageModelProvider,
		DecisionModel:               kernel.decisionModel,
		LLMCallRepository:           llmCallRepositoryOf(services),
	})
}

func llmCallRepositoryOf(services taskServices) taskstate.LLMCallRepository {
	if services.repositories.llmCall == nil {
		return nil
	}
	return services.repositories.llmCall
}

func newCapabilityClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capabilityConfiguration(runtimeConfiguration))
}

func capabilityConfiguration(runtimeConfiguration config.RuntimeConfiguration) capability.Configuration {
	return capability.Configuration{
		Endpoint:       runtimeConfiguration.Capabilities.Endpoint,
		UnixSocketPath: runtimeConfiguration.Capabilities.UnixSocketPath,
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
