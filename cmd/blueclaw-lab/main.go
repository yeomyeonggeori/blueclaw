package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/bluecollarharness"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/e2e"
	"github.com/yeomyeonggeori/blueclaw/internal/lab"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

type PrintingCommandRunner struct{}

func (printingCommandRunner PrintingCommandRunner) Run(ctx context.Context, executableCommand lab.ExecutableCommand) error {
	_ = ctx
	printExecutableCommand(executableCommand)
	return nil
}

func (printingCommandRunner PrintingCommandRunner) Start(ctx context.Context, executableCommand lab.ExecutableCommand) error {
	_ = ctx
	printExecutableCommand(executableCommand)
	return nil
}

func (printingCommandRunner PrintingCommandRunner) Output(ctx context.Context, executableCommand lab.ExecutableCommand) (string, error) {
	_ = ctx
	printExecutableCommand(executableCommand)
	return "127.0.0.1", nil
}

func init() {
	e2e.UseAgentHarnessFactory(bluecollarharness.New)
}

func main() {
	configurationPath := flag.String("configuration", "config/lab.example.json", "lab configuration path")
	mode := flag.String("mode", "", "lab mode override")
	dryRun := flag.Bool("dry-run", false, "print commands without executing them")
	virtualScenarioName := flag.String("scenario", "presentation", "virtual session scenario name")
	virtualArtifactDirectoryPath := flag.String("artifact-dir", ".artifacts/blueclaw-e2e", "virtual session artifact directory")
	flag.Parse()

	if flag.NArg() == 0 {
		log.Fatal("lab command is required")
	}

	configuration, errorValue := lab.LoadConfiguration(*configurationPath)
	if errorValue != nil {
		log.Fatal(errorValue)
	}
	if *mode != "" {
		configuration.Host.Mode = *mode
	}

	commandRunner := lab.CommandRunner(lab.OperatingSystemCommandRunner{})
	if *dryRun {
		commandRunner = PrintingCommandRunner{}
	}

	repositoryRootPath, errorValue := os.Getwd()
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	service := lab.NewService(configuration, commandRunner, repositoryRootPath)
	ctx := context.Background()

	commandName := flag.Arg(0)
	switch commandName {
	case "image-build":
		errorValue = service.ImageBuild(ctx)
	case "vm-up":
		errorValue = service.VirtualMachineUp(ctx)
	case "vm-down":
		errorValue = service.VirtualMachineDown(ctx)
	case "vm-ssh":
		errorValue = service.VirtualMachineSSH(ctx, flag.Args()[1:])
	case "scenario-browser-handoff":
		errorValue = service.ScenarioBrowserHandoff(ctx)
	case "scenario-mattermost":
		errorValue = service.ScenarioMattermost(ctx)
	case "scenario-slack":
		errorValue = service.ScenarioSlack(ctx)
	case "virtual-session":
		virtualSessionArguments, parseError := parseVirtualSessionArguments(flag.Args()[1:], *virtualScenarioName, *virtualArtifactDirectoryPath)
		if parseError != nil {
			errorValue = parseError
		} else {
			errorValue = runVirtualSession(ctx, virtualSessionArguments)
		}
	default:
		errorValue = fmt.Errorf("unsupported lab command: %s", commandName)
	}
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}

// The tools these scenarios drive belong to a product, which offers its catalog
// through the environment. A standalone checkout is offered none, so it says so
// and runs nothing rather than failing on tools it was never given.
func virtualSessionSkipReason() string {
	foundTools, missingTools := e2e.ScenarioCapabilityAvailability()
	if len(missingTools) == 0 {
		return ""
	}
	if len(foundTools) == 0 {
		return "skipping the virtual session: " + e2e.ScenarioCapabilityCatalogVariable + " names no capability tool catalog"
	}
	return "skipping the virtual session: the catalog in " + e2e.ScenarioCapabilityCatalogVariable +
		" carries no descriptor for " + strings.Join(missingTools, ", ")
}

type virtualSessionArguments struct {
	ScenarioName          string
	ScenarioFilePath      string
	ArtifactDirectoryPath string
	LanguageModelEndpoint string
	LanguageModelSocket   string
	LanguageModelProvider string
	LanguageModelName     string
	EmbeddingEndpoint     string
	ExecutionMode         string
	SkillDirectoryPath    string
	Seed                  *int64
	Temperature           *float64
	LiveLanguageModel     bool
	StrictAssertions      bool
	ValidateOnly          bool
	MaximumModelTier      string
	RealModelTiers        bool
}

type virtualSessionEvidence struct {
	Scenario              string                              `json:"scenario"`
	Status                string                              `json:"status"`
	RequestedProvider     string                              `json:"requestedProvider"`
	RequestedModel        string                              `json:"requestedModel,omitempty"`
	ExecutionMode         string                              `json:"executionMode,omitempty"`
	MaximumModelTier      string                              `json:"maximumModelTier,omitempty"`
	RealModelTiers        bool                                `json:"realModelTiers,omitempty"`
	StructuredSchemaNames []string                            `json:"structuredSchemaNames,omitempty"`
	Calls                 []e2e.VirtualLanguageModelCallEvent `json:"calls"`
	TurnMetrics           []virtualTurnMetrics                `json:"turnMetrics"`
}

type virtualSessionResultEvidence struct {
	Status   string                   `json:"status"`
	RunError string                   `json:"runError,omitempty"`
	Result   e2e.VirtualSessionResult `json:"result"`
}

type virtualTurnMetrics struct {
	TurnNumber              int                                 `json:"turnNumber"`
	TaskRunID               string                              `json:"taskRunID,omitempty"`
	TaskStatus              string                              `json:"taskStatus,omitempty"`
	FailureReason           string                              `json:"failureReason,omitempty"`
	AgentStepCount          int                                 `json:"agentStepCount"`
	ToolCallCount           int                                 `json:"toolCallCount"`
	LanguageModelCallCount  int                                 `json:"languageModelCallCount"`
	LanguageModelLatencyMS  int64                               `json:"languageModelLatencyMs"`
	TaskDurationMS          int64                               `json:"taskDurationMs,omitempty"`
	InformationalAssertions []e2e.VirtualInformationalAssertion `json:"informationalAssertions,omitempty"`
}

func parseVirtualSessionArguments(arguments []string, defaultScenarioName string, defaultArtifactDirectoryPath string) (virtualSessionArguments, error) {
	flagSet := flag.NewFlagSet("virtual-session", flag.ContinueOnError)
	scenarioName := flagSet.String("scenario", defaultScenarioName, "virtual session scenario name")
	scenarioFilePath := flagSet.String("scenario-file", "", "file-backed sequential virtual session scenario")
	artifactDirectoryPath := flagSet.String("artifact-dir", defaultArtifactDirectoryPath, "virtual session artifact directory")
	languageModelEndpoint := flagSet.String("llm-endpoint", os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"), "live LLM capability endpoint")
	languageModelSocket := flagSet.String("llm-unix-socket", os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"), "live LLM capability unix socket path")
	languageModelProvider := flagSet.String("llm-provider", firstNonEmptyString(os.Getenv("BLUECLAW_E2E_LLM_PROVIDER"), "openrouter"), "live LLM provider: openrouter, direct, or capability")
	languageModelName := flagSet.String("llm-model", os.Getenv("BLUECLAW_E2E_LLM_MODEL"), "live LLM model override")
	embeddingEndpoint := flagSet.String("embedding-endpoint", os.Getenv("BLUECLAW_E2E_EMBEDDING_ENDPOINT"), "local OpenAI-compatible embedding endpoint")
	executionMode := flagSet.String("llm-execution-mode", firstNonEmptyString(os.Getenv("BLUECLAW_E2E_LLM_EXECUTION_MODE"), "auto"), "live LLM execution mode")
	seed := flagSet.Int64("seed", 0, "generation seed for live LLM calls")
	temperature := flagSet.Float64("temperature", 0, "generation temperature for live LLM calls")
	skillDirectoryPath := flagSet.String("skill-dir", "", "skill directory to load into the virtual workspace")
	liveLanguageModel := flagSet.Bool("live-llm", truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")), "explicitly allow costed live LLM calls for unscripted scenarios")
	strictAssertions := flagSet.Bool("strict-assertions", false, "fail when any declared step expectation is not satisfied")
	validateOnly := flagSet.Bool("validate-only", false, "validate the scenario file without running it")
	maximumModelTier := flagSet.String("maximum-model-tier", "", "maximum live model tier: xlow, low, medium, high, xhigh, or max")
	realModelTiers := flagSet.Bool("real-model-tiers", false, "use the production model tier configuration without a ceiling")
	if errorValue := flagSet.Parse(arguments); errorValue != nil {
		return virtualSessionArguments{}, errorValue
	}
	normalizedMaximumModelTier, errorValue := normalizeVirtualMaximumModelTier(*maximumModelTier)
	if errorValue != nil {
		return virtualSessionArguments{}, errorValue
	}
	if *realModelTiers && normalizedMaximumModelTier != "" {
		return virtualSessionArguments{}, errors.New("--real-model-tiers cannot be combined with --maximum-model-tier")
	}
	if (*realModelTiers || normalizedMaximumModelTier != "") && hasVirtualSessionFlag(arguments, "llm-model") {
		return virtualSessionArguments{}, errors.New("tiered model execution cannot be combined with --llm-model")
	}
	return virtualSessionArguments{
		ScenarioName:          *scenarioName,
		ScenarioFilePath:      strings.TrimSpace(*scenarioFilePath),
		ArtifactDirectoryPath: *artifactDirectoryPath,
		LanguageModelEndpoint: *languageModelEndpoint,
		LanguageModelSocket:   *languageModelSocket,
		LanguageModelProvider: *languageModelProvider,
		LanguageModelName:     *languageModelName,
		EmbeddingEndpoint:     strings.TrimSpace(*embeddingEndpoint),
		ExecutionMode:         *executionMode,
		SkillDirectoryPath:    *skillDirectoryPath,
		Seed:                  virtualSessionInt64FlagPointer(arguments, "seed", *seed),
		Temperature:           virtualSessionFloat64FlagPointer(arguments, "temperature", *temperature),
		LiveLanguageModel:     *liveLanguageModel,
		StrictAssertions:      *strictAssertions,
		ValidateOnly:          *validateOnly,
		MaximumModelTier:      normalizedMaximumModelTier,
		RealModelTiers:        *realModelTiers,
	}, nil
}

func runVirtualSession(ctx context.Context, arguments virtualSessionArguments) error {
	if skipReason := virtualSessionSkipReason(); skipReason != "" {
		fmt.Fprintln(os.Stderr, skipReason)
		return nil
	}
	scenario, errorValue := loadVirtualSessionScenario(arguments)
	if errorValue != nil {
		return errorValue
	}
	if arguments.ValidateOnly {
		return nil
	}
	var embeddingObserver *observedEmbeddingProvider
	if arguments.LiveLanguageModel {
		languageModel, errorValue := createLiveLanguageModel(arguments)
		if errorValue != nil {
			return errorValue
		}
		var languageModelFactoryError error
		languageModelFactory := func(modelName string) llm.LanguageModelProvider {
			modelArguments := arguments
			modelArguments.LanguageModelName = modelName
			modelProvider, factoryError := createLiveLanguageModel(modelArguments)
			if factoryError != nil {
				languageModelFactoryError = errors.Join(languageModelFactoryError, factoryError)
				return nil
			}
			return modelProvider
		}
		scenario.LanguageModel = languageModel
		embeddingProvider := liveEmbeddingProvider(arguments)
		embeddingObserver = &observedEmbeddingProvider{provider: embeddingProvider}
		scenario.EmbeddingProvider = embeddingObserver
		scenario.EmbeddingModel = llm.DefaultEmbeddingModelName
		if arguments.RealModelTiers || arguments.MaximumModelTier != "" {
			configureVirtualScenarioModelTiers(&scenario, arguments.MaximumModelTier, languageModelFactory)
			if languageModelFactoryError != nil {
				return languageModelFactoryError
			}
		}
		scenario.DisableScriptedModel = true
		scenario.UseLooseAssertions = !arguments.StrictAssertions
		scenario.FailOnLanguageModelError = arguments.StrictAssertions
		scenario.ProgressWriter = os.Stderr
		delayLiveVirtualSession()
	} else if isLiveVirtualScenario(scenario) {
		return errors.New("virtual-session scenario needs live LLM calls; pass --live-llm or set BLUECLAW_E2E_LIVE=1")
	}
	if arguments.LiveLanguageModel {
		if skillDirectoryPaths := liveSkillDirectoryPaths(arguments, scenario); len(skillDirectoryPaths) > 0 {
			scenario.Skills = nil
			scenario.SkillDirectoryPaths = skillDirectoryPaths
		}
	}
	result, errorValue := e2e.RunVirtualSession(ctx, scenario)
	evidenceError := saveVirtualSessionEvidence(arguments, result, errorValue)
	printVirtualSessionResult(result)
	if errorValue != nil {
		return errorValue
	}
	if evidenceError != nil {
		return evidenceError
	}
	if arguments.LiveLanguageModel && arguments.StrictAssertions && len(scenario.SkillDirectoryPaths) > 0 && embeddingObserver.successfulCallCount.Load() == 0 {
		return errors.New("strict live scenario did not complete a local BGE-M3 embedding call")
	}
	if arguments.LiveLanguageModel && arguments.StrictAssertions && len(scenario.SkillDirectoryPaths) > 0 {
		if errorValue := validateStrictEmbeddingRetrieval(result); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func validateStrictEmbeddingRetrieval(result e2e.VirtualSessionResult) error {
	foundRetrievalEvidence := false
	foundReadyEmbedding := false
	for _, turnResult := range result.TurnResults {
		for _, event := range turnResult.Events {
			if event.Name != "agent.instructions_loaded" {
				continue
			}
			foundRetrievalEvidence = true
			var retrieval struct {
				Mode        string `json:"retrievalMode"`
				IndexStatus string `json:"indexStatus"`
			}
			if errorValue := json.Unmarshal([]byte(event.Body), &retrieval); errorValue != nil {
				return errors.New("strict live scenario could not verify skill retrieval mode")
			}
			switch retrieval.Mode {
			case "embedding":
				if retrieval.IndexStatus != "ready" {
					return fmt.Errorf("strict live scenario used embedding skill retrieval with index status %s", firstNonEmptyString(retrieval.IndexStatus, "unknown"))
				}
				foundReadyEmbedding = true
			case "direct":
				if retrieval.IndexStatus != "bypassed" {
					return fmt.Errorf("strict live scenario used direct skill retrieval with index status %s", firstNonEmptyString(retrieval.IndexStatus, "unknown"))
				}
			case "structured_query":
				if retrieval.IndexStatus != "empty_query" {
					return fmt.Errorf("strict live scenario used structured skill retrieval with index status %s", firstNonEmptyString(retrieval.IndexStatus, "unknown"))
				}
			default:
				return fmt.Errorf("strict live scenario used %s skill retrieval with index status %s", firstNonEmptyString(retrieval.Mode, "unknown"), firstNonEmptyString(retrieval.IndexStatus, "unknown"))
			}
		}
	}
	if !foundRetrievalEvidence {
		return errors.New("strict live scenario did not record skill retrieval evidence")
	}
	if !foundReadyEmbedding {
		return errors.New("strict live scenario did not record ready embedding retrieval evidence")
	}
	return nil
}

type observedEmbeddingProvider struct {
	provider            llm.EmbeddingProvider
	successfulCallCount atomic.Int64
}

func (provider *observedEmbeddingProvider) GenerateEmbedding(ctx context.Context, input string) ([]float32, error) {
	embedding, errorValue := provider.provider.GenerateEmbedding(ctx, input)
	if errorValue == nil && len(embedding) > 0 {
		provider.successfulCallCount.Add(1)
	}
	return embedding, errorValue
}

func liveEmbeddingProvider(arguments virtualSessionArguments) llm.EmbeddingProvider {
	if strings.TrimSpace(arguments.EmbeddingEndpoint) != "" {
		return llm.OpenAIEmbeddingClient{
			Endpoint:  arguments.EmbeddingEndpoint,
			ModelName: llm.DefaultEmbeddingModelName,
		}
	}
	return llm.CapabilityEmbeddingClient{
		CapabilityClient: capability.NewClient(capability.Configuration{
			Endpoint:       endpointForVirtualSession(arguments),
			UnixSocketPath: arguments.LanguageModelSocket,
		}),
		ModelName:     llm.DefaultEmbeddingModelName,
		ExecutionMode: arguments.ExecutionMode,
	}
}

func printVirtualSessionResult(result e2e.VirtualSessionResult) {
	if strings.TrimSpace(result.ScenarioName) == "" {
		return
	}
	fmt.Println("scenario:", result.ScenarioName)
	fmt.Println("artifactDirectoryPath:", result.ArtifactDirectoryPath)
	for index, turnResult := range result.TurnResults {
		fmt.Printf("turn %d taskRunID: %s\n", index+1, turnResult.TaskRunID)
		fmt.Printf("turn %d taskStatus: %s\n", index+1, turnResult.TaskStatus)
		if strings.TrimSpace(turnResult.FailureReason) != "" {
			fmt.Printf("turn %d failure: %s\n", index+1, turnResult.FailureReason)
			printVirtualTurnFailureEvents(index+1, turnResult)
		}
		fmt.Printf("turn %d reply: %s\n", index+1, turnResult.FinishMessage)
		for _, summary := range languageModelCallFailureSummaries(turnResult) {
			fmt.Printf("turn %d llm.call error: %s\n", index+1, summary)
		}
		for _, assertion := range turnResult.InformationalAssertions {
			fmt.Printf("turn %d informational assertion %s: %t (%s)\n", index+1, assertion.Name, assertion.Satisfied, assertion.Detail)
		}
		for _, attachment := range turnResult.Attachments {
			fmt.Printf("turn %d attachment: %s\n", index+1, attachment.DevicePath)
		}
	}
}

func saveVirtualSessionEvidence(arguments virtualSessionArguments, result e2e.VirtualSessionResult, runError error) error {
	if strings.TrimSpace(result.ArtifactDirectoryPath) == "" {
		return errors.New("virtual session artifact directory is required for LLM evidence")
	}
	status := virtualSessionEvidenceStatus(result, runError)
	evidence := virtualSessionEvidence{
		Scenario:          result.ScenarioName,
		Status:            status,
		RequestedProvider: strings.TrimSpace(arguments.LanguageModelProvider),
		RequestedModel:    strings.TrimSpace(arguments.LanguageModelName),
		ExecutionMode:     strings.TrimSpace(arguments.ExecutionMode),
		MaximumModelTier:  strings.TrimSpace(arguments.MaximumModelTier),
		RealModelTiers:    arguments.RealModelTiers,
		Calls:             virtualSessionLanguageModelCalls(result),
		TurnMetrics:       virtualSessionTurnMetrics(result),
	}
	resultEvidence := virtualSessionResultEvidence{
		Status:   status,
		RunError: errorString(runError),
		Result:   result,
	}
	if errorValue := writeVirtualSessionJSON(filepath.Join(result.ArtifactDirectoryPath, "result.json"), resultEvidence); errorValue != nil {
		return errorValue
	}
	return writeVirtualSessionJSON(filepath.Join(result.ArtifactDirectoryPath, "llm-routing-evidence.json"), evidence)
}

func writeVirtualSessionJSON(path string, value any) error {
	document, errorValue := json.MarshalIndent(value, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	return os.WriteFile(path, append(document, '\n'), 0600)
}

func errorString(errorValue error) string {
	if errorValue == nil {
		return ""
	}
	return errorValue.Error()
}

func virtualSessionEvidenceStatus(result e2e.VirtualSessionResult, runError error) string {
	if runError != nil {
		return "failed"
	}
	if len(result.TurnResults) == 0 {
		return "succeeded"
	}
	switch result.TurnResults[len(result.TurnResults)-1].TaskStatus {
	case task.TaskStatusFailed, task.TaskStatusBlocked, task.TaskStatusCancelled, task.TaskStatusInterrupted:
		return "failed"
	case task.TaskStatusPlanned, task.TaskStatusRunning, task.TaskStatusWaitingUserInput, task.TaskStatusWaitingApproval:
		return "incomplete"
	default:
		return "succeeded"
	}
}

func virtualSessionTurnMetrics(result e2e.VirtualSessionResult) []virtualTurnMetrics {
	metrics := make([]virtualTurnMetrics, 0, len(result.TurnResults))
	for turnIndex, turnResult := range result.TurnResults {
		metrics = append(metrics, buildVirtualTurnMetrics(turnIndex+1, turnResult))
	}
	return metrics
}

func buildVirtualTurnMetrics(turnNumber int, turnResult e2e.VirtualTurnResult) virtualTurnMetrics {
	metrics := virtualTurnMetrics{
		TurnNumber:              turnNumber,
		TaskRunID:               turnResult.TaskRunID,
		TaskStatus:              string(turnResult.TaskStatus),
		FailureReason:           strings.TrimSpace(turnResult.FailureReason),
		LanguageModelCallCount:  len(turnResult.LanguageModelCallEvents),
		InformationalAssertions: turnResult.InformationalAssertions,
	}
	for _, call := range turnResult.LanguageModelCallEvents {
		metrics.LanguageModelLatencyMS += call.LatencyMS
	}
	for _, event := range turnResult.Events {
		if event.Name == "agent.action" {
			metrics.AgentStepCount++
		}
		if strings.HasPrefix(event.Name, "tool.") && strings.HasSuffix(event.Name, ".requested") {
			metrics.ToolCallCount++
		}
		if event.Name == "blueclaw.task.execution_duration" {
			metrics.TaskDurationMS = taskDurationMilliseconds(event.Body)
		}
	}
	return metrics
}

func taskDurationMilliseconds(body string) int64 {
	var document struct {
		DurationMS int64 `json:"durationMs"`
	}
	if json.Unmarshal([]byte(body), &document) != nil {
		return 0
	}
	return document.DurationMS
}

func virtualSessionLanguageModelCalls(result e2e.VirtualSessionResult) []e2e.VirtualLanguageModelCallEvent {
	calls := []e2e.VirtualLanguageModelCallEvent{}
	for _, turnResult := range result.TurnResults {
		calls = append(calls, turnResult.LanguageModelCallEvents...)
	}
	return calls
}

func printVirtualTurnFailureEvents(turnNumber int, turnResult e2e.VirtualTurnResult) {
	for _, event := range turnResult.Events {
		if !isVirtualFailureDiagnosticEvent(event.Name) {
			continue
		}
		body := event.Body
		if len(body) > 800 {
			body = body[:800] + "..."
		}
		fmt.Printf("turn %d event %s: %s\n", turnNumber, event.Name, body)
	}
}

func isVirtualFailureDiagnosticEvent(eventName string) bool {
	return strings.HasPrefix(eventName, "tool.") ||
		eventName == "agent.action" ||
		strings.Contains(eventName, "tool_palette") ||
		strings.Contains(eventName, "step_working_set") ||
		strings.Contains(eventName, "completion") ||
		strings.Contains(eventName, "finalizer") ||
		strings.Contains(eventName, "stall") ||
		strings.Contains(eventName, "no_progress")
}

type virtualModelTierProviders struct {
	xLow   llm.LanguageModelProvider
	low    llm.LanguageModelProvider
	medium llm.LanguageModelProvider
	high   llm.LanguageModelProvider
	xHigh  llm.LanguageModelProvider
	max    llm.LanguageModelProvider
}

type virtualModelTierNames struct {
	xLow   string
	low    string
	medium string
	high   string
	xHigh  string
	max    string
}

func configureVirtualScenarioModelTiers(scenario *e2e.VirtualSessionScenario, maximumModelTier string, providerFactory func(string) llm.LanguageModelProvider) {
	tierNames := defaultVirtualModelTierNames()
	if maximumModelTier == "" {
		providers := buildUncappedVirtualModelTierProviders(tierNames, providerFactory)
		applyVirtualModelTierProviders(scenario, providers, fallbackVirtualModelProvider(
			llm.WithModelTier(providerFactory(tierNames.medium), "medium"),
			llm.WithModelTier(providerFactory(tierNames.high), "high"),
			"intake", "high",
		))
		return
	}
	providers := buildCappedVirtualModelTierProviders(tierNames, providerFactory)
	maximumProvider := providers.providerForTier(maximumModelTier)
	applyVirtualModelTierProviders(scenario, virtualModelTierProviders{
		xLow: providers.providerAtOrBelow("xlow", maximumModelTier), low: providers.providerAtOrBelow("low", maximumModelTier),
		medium: providers.providerAtOrBelow("medium", maximumModelTier), high: providers.providerAtOrBelow("high", maximumModelTier),
		xHigh: providers.providerAtOrBelow("xhigh", maximumModelTier), max: providers.providerAtOrBelow("max", maximumModelTier),
	}, maximumProvider)
}

func applyVirtualModelTierProviders(scenario *e2e.VirtualSessionScenario, providers virtualModelTierProviders, intakeProvider llm.LanguageModelProvider) {
	scenario.LanguageModel = providers.low
	scenario.XLowLanguageModel = providers.xLow
	scenario.MediumLanguageModel = providers.medium
	scenario.HighLanguageModel = providers.high
	scenario.XHighLanguageModel = providers.xHigh
	scenario.MaxLanguageModel = providers.max
	scenario.IntakeLanguageModel = intakeProvider
}

func buildUncappedVirtualModelTierProviders(tierNames virtualModelTierNames, providerFactory func(string) llm.LanguageModelProvider) virtualModelTierProviders {
	xLowModel := llm.WithModelTier(providerFactory(tierNames.xLow), "xlow")
	lowModel := llm.WithModelTier(providerFactory(tierNames.low), "low")
	mediumModel := llm.WithModelTier(providerFactory(tierNames.medium), "medium")
	highModel := llm.WithModelTier(providerFactory(tierNames.high), "high")
	xHighModel := llm.WithModelTier(providerFactory(tierNames.xHigh), "xhigh")
	maxModel := llm.WithModelTier(providerFactory(tierNames.max), "max")
	lowProvider := fallbackVirtualModelProvider(lowModel, mediumModel, "low", "medium")
	xLowProvider := fallbackVirtualModelProvider(xLowModel, lowProvider, "xlow", "low")
	mediumProvider := fallbackVirtualModelProvider(mediumModel, lowModel, "medium", "low")
	highProvider := fallbackVirtualModelProvider(highModel, mediumProvider, "high", "medium")
	xHighProvider := fallbackVirtualModelProvider(xHighModel, highProvider, "xhigh", "high")
	maxProvider := fallbackVirtualModelProvider(maxModel, xHighProvider, "max", "xhigh")
	return virtualModelTierProviders{xLow: xLowProvider, low: lowProvider, medium: mediumProvider, high: highProvider, xHigh: xHighProvider, max: maxProvider}
}

func buildCappedVirtualModelTierProviders(tierNames virtualModelTierNames, providerFactory func(string) llm.LanguageModelProvider) virtualModelTierProviders {
	xLowModel := llm.WithModelTier(providerFactory(tierNames.xLow), "xlow")
	lowProvider := fallbackVirtualModelProvider(llm.WithModelTier(providerFactory(tierNames.low), "low"), xLowModel, "low", "xlow")
	xLowProvider := llm.VisionFallbackProvider{TextOnlyModel: xLowModel, VisionModel: lowProvider}
	mediumProvider := fallbackVirtualModelProvider(llm.WithModelTier(providerFactory(tierNames.medium), "medium"), lowProvider, "medium", "low")
	highProvider := fallbackVirtualModelProvider(llm.WithModelTier(providerFactory(tierNames.high), "high"), mediumProvider, "high", "medium")
	xHighProvider := fallbackVirtualModelProvider(llm.WithModelTier(providerFactory(tierNames.xHigh), "xhigh"), highProvider, "xhigh", "high")
	maxProvider := fallbackVirtualModelProvider(llm.WithModelTier(providerFactory(tierNames.max), "max"), xHighProvider, "max", "xhigh")
	return virtualModelTierProviders{xLow: xLowProvider, low: lowProvider, medium: mediumProvider, high: highProvider, xHigh: xHighProvider, max: maxProvider}
}

func fallbackVirtualModelProvider(primaryProvider llm.LanguageModelProvider, fallbackProvider llm.LanguageModelProvider, primaryLabel string, fallbackLabel string) llm.LanguageModelProvider {
	return llm.FallbackLanguageModelProvider{PrimaryProvider: primaryProvider, FallbackProvider: fallbackProvider, PrimaryLabel: primaryLabel, FallbackLabel: fallbackLabel}
}

func (providers virtualModelTierProviders) providerAtOrBelow(requestedTier string, maximumModelTier string) llm.LanguageModelProvider {
	if virtualModelTierRank(requestedTier) > virtualModelTierRank(maximumModelTier) {
		return providers.providerForTier(maximumModelTier)
	}
	return providers.providerForTier(requestedTier)
}

func (providers virtualModelTierProviders) providerForTier(modelTier string) llm.LanguageModelProvider {
	switch modelTier {
	case "max":
		return providers.max
	case "xhigh":
		return providers.xHigh
	case "high":
		return providers.high
	case "medium":
		return providers.medium
	case "low":
		return providers.low
	default:
		return providers.xLow
	}
}

func normalizeVirtualMaximumModelTier(modelTier string) (string, error) {
	normalizedModelTier := strings.ToLower(strings.TrimSpace(modelTier))
	if normalizedModelTier == "" {
		return "", nil
	}
	for _, supportedModelTier := range []string{"xlow", "low", "medium", "high", "xhigh", "max"} {
		if normalizedModelTier == supportedModelTier {
			return normalizedModelTier, nil
		}
	}
	return "", fmt.Errorf("maximum model tier must be xlow, low, medium, high, xhigh, or max: %s", modelTier)
}

func virtualModelTierRank(modelTier string) int {
	for rank, supportedModelTier := range []string{"xlow", "low", "medium", "high", "xhigh", "max"} {
		if modelTier == supportedModelTier {
			return rank
		}
	}
	return 0
}

func defaultVirtualModelTierNames() virtualModelTierNames {
	tierNames := llm.ResolveModelTierNames(config.RuntimeConfiguration{})
	return virtualModelTierNames{
		xLow: tierNames.XLow, low: tierNames.Low, medium: tierNames.Medium,
		high: tierNames.High, xHigh: tierNames.XHigh, max: tierNames.Max,
	}
}

func loadVirtualSessionScenario(arguments virtualSessionArguments) (e2e.VirtualSessionScenario, error) {
	if strings.TrimSpace(arguments.ScenarioFilePath) != "" {
		return e2e.LoadScenarioFile(arguments.ScenarioFilePath, arguments.ArtifactDirectoryPath)
	}
	return e2e.BuiltinScenario(arguments.ScenarioName, arguments.ArtifactDirectoryPath)
}

const defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"

func createLiveLanguageModel(arguments virtualSessionArguments) (llm.LanguageModelProvider, error) {
	generationOptions := llm.GenerationOptions{Seed: arguments.Seed, Temperature: arguments.Temperature}
	modelName := liveLanguageModelName(arguments.LanguageModelName)
	switch strings.TrimSpace(arguments.LanguageModelProvider) {
	case "openrouter", "":
		openRouterAPIKey, errorValue := resolveOpenRouterAPIKey()
		if errorValue != nil {
			return nil, errorValue
		}
		return llm.OpenRouterClient{
			APIKey:            openRouterAPIKey,
			BaseURL:           firstNonEmptyString(os.Getenv("OPENROUTER_BASE_URL"), llm.DefaultOpenRouterChatCompletionsURL),
			ModelName:         modelName,
			AttemptCount:      3,
			InitialBackoff:    750 * time.Millisecond,
			GenerationOptions: generationOptions,
		}, nil
	case "direct":
		openRouterAPIKey, errorValue := resolveOpenRouterAPIKey()
		if errorValue != nil {
			return nil, errorValue
		}
		return openaicompatible.NewProvider(
			firstNonEmptyString(os.Getenv("OPENROUTER_BASE_URL"), defaultOpenRouterBaseURL),
			openRouterAPIKey,
			modelName,
		), nil
	case "capability":
		return llm.CapabilityLLMClient{
			CapabilityClient: capability.NewClient(capability.Configuration{
				Endpoint:       arguments.LanguageModelEndpoint,
				UnixSocketPath: arguments.LanguageModelSocket,
			}),
			ModelName:     modelName,
			ExecutionMode: arguments.ExecutionMode,
		}, nil
	default:
		return nil, errors.New("live LLM provider must be openrouter, direct, or capability")
	}
}

func optionalLiveOpenRouterLanguageModel(arguments virtualSessionArguments) llm.LanguageModelProvider {
	openRouterAPIKey, errorValue := resolveOpenRouterAPIKey()
	if errorValue != nil {
		return nil
	}
	return llm.OpenRouterClient{
		APIKey:            openRouterAPIKey,
		BaseURL:           firstNonEmptyString(os.Getenv("OPENROUTER_BASE_URL"), llm.DefaultOpenRouterChatCompletionsURL),
		ModelName:         liveLanguageModelName(arguments.LanguageModelName),
		AttemptCount:      3,
		InitialBackoff:    750 * time.Millisecond,
		GenerationOptions: llm.GenerationOptions{Seed: arguments.Seed, Temperature: arguments.Temperature},
	}
}

func languageModelCallFailureSummaries(turnResult e2e.VirtualTurnResult) []string {
	summaries := []string{}
	for _, event := range turnResult.LanguageModelCallEvents {
		if !event.IsError {
			continue
		}
		summaries = append(summaries, strings.TrimSpace(strings.Join([]string{event.Kind, event.SchemaName, event.Error}, " ")))
	}
	return summaries
}

func hasVirtualSessionFlag(arguments []string, name string) bool {
	longName := "--" + name
	shortName := "-" + name
	for _, argument := range arguments {
		if argument == longName || argument == shortName {
			return true
		}
		if strings.HasPrefix(argument, longName+"=") || strings.HasPrefix(argument, shortName+"=") {
			return true
		}
	}
	return false
}

func virtualSessionInt64FlagPointer(arguments []string, name string, value int64) *int64 {
	if !hasVirtualSessionFlag(arguments, name) {
		return nil
	}
	result := value
	return &result
}

func virtualSessionFloat64FlagPointer(arguments []string, name string, value float64) *float64 {
	if !hasVirtualSessionFlag(arguments, name) {
		return nil
	}
	result := value
	return &result
}

func resolveOpenRouterAPIKey() (string, error) {
	openRouterAPIKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if openRouterAPIKey != "" {
		return openRouterAPIKey, nil
	}
	homeDirectoryPath, errorValue := os.UserHomeDir()
	if errorValue != nil {
		return "", errorValue
	}
	document, errorValue := os.ReadFile(filepath.Join(homeDirectoryPath, ".blueclaw", "openrouter_api_key"))
	if errorValue != nil {
		return "", errors.New("OPENROUTER_API_KEY is required or ~/.blueclaw/openrouter_api_key must exist")
	}
	openRouterAPIKey = strings.TrimSpace(strings.TrimPrefix(string(document), "OPENROUTER_API_KEY="))
	if openRouterAPIKey == "" {
		return "", errors.New("OpenRouter API key file is empty")
	}
	return openRouterAPIKey, nil
}

var delayLiveVirtualSession = func() {
	time.Sleep(1500 * time.Millisecond)
}

func truthyEnvironmentValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func endpointForVirtualSession(arguments virtualSessionArguments) string {
	return strings.TrimSpace(arguments.LanguageModelEndpoint)
}

func isLiveVirtualScenario(scenario e2e.VirtualSessionScenario) bool {
	for _, virtualTurn := range scenario.Turns {
		if len(virtualTurn.ActionResponses) > 0 {
			return false
		}
	}
	return true
}

func liveSkillDirectoryPaths(arguments virtualSessionArguments, scenario e2e.VirtualSessionScenario) []string {
	if skillDirectoryPath := strings.TrimSpace(arguments.SkillDirectoryPath); skillDirectoryPath != "" {
		return []string{skillDirectoryPath}
	}
	skillDirectoryPaths := []string{}
	for _, skillInstruction := range scenario.Skills {
		for _, skillRootPath := range e2e.ScenarioSkillRootPaths() {
			candidatePath := filepath.Join(skillRootPath, skillInstruction.Name)
			if information, errorValue := os.Stat(candidatePath); errorValue == nil && information.IsDir() {
				skillDirectoryPaths = append(skillDirectoryPaths, candidatePath)
				break
			}
		}
	}
	return skillDirectoryPaths
}

func liveLanguageModelName(modelOverride string) string {
	if modelName := strings.TrimSpace(modelOverride); modelName != "" {
		return modelName
	}
	return llm.ResolveModelTierNames(config.RuntimeConfiguration{}).XLow
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func printExecutableCommand(executableCommand lab.ExecutableCommand) {
	parts := []string{executableCommand.ExecutableName}
	parts = append(parts, executableCommand.Arguments...)
	if executableCommand.StandardInputPath != "" {
		parts = append(parts, "<", executableCommand.StandardInputPath)
	}

	fmt.Println(strings.Join(parts, " "))
}
