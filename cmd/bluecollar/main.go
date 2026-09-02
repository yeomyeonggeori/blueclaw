// Command bluecollar runs the agent loop as a program: point it at a directory,
// give it a model, hand it a task, read the answer. No appliance around it - no
// database, no connectors, no policy, no POSIX projection - so a benchmark can
// drive it the way it drives any other coding agent.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

const defaultRequesterPersonID = "bluecollar"

type runOptions struct {
	WorkspacePath string
	Task          string
	ModelName     string
	Endpoint      string
	APIKey        string
	TurnTimeout   time.Duration
	ResultPath    string
}

func main() {
	options, errorValue := parseRunOptions()
	if errorValue != nil {
		fmt.Fprintln(os.Stderr, errorValue)
		os.Exit(2)
	}
	result, errorValue := runTask(options)
	if errorValue != nil {
		fmt.Fprintln(os.Stderr, errorValue)
		os.Exit(1)
	}
	document, _ := json.MarshalIndent(result, "", "  ")
	if strings.TrimSpace(options.ResultPath) != "" {
		if writeError := os.WriteFile(options.ResultPath, document, 0o644); writeError != nil {
			fmt.Fprintln(os.Stderr, writeError)
			os.Exit(1)
		}
	}
	fmt.Println(string(document))
	if !result.Answered {
		os.Exit(1)
	}
}

func parseRunOptions() (runOptions, error) {
	workspacePath := flag.String("workspace", ".", "directory the agent works in")
	modelName := flag.String("model", "", "model name to pin, for example openai/gpt-5.6-luna")
	endpoint := flag.String("llm-endpoint", os.Getenv("BLUECLAW_LLM_ENDPOINT"), "OpenAI-compatible chat completions base URL")
	apiKeyPath := flag.String("llm-api-key-path", os.Getenv("BLUECLAW_LLM_API_KEY_PATH"), "file holding the model endpoint api key")
	turnTimeout := flag.Duration("timeout", 30*time.Minute, "how long one task may run")
	resultPath := flag.String("result-json", "", "also write the result document here")
	flag.Parse()

	taskText := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if taskText == "" {
		return runOptions{}, fmt.Errorf("a task is required: bluecollar --workspace <dir> \"<task>\"")
	}
	absoluteWorkspacePath, errorValue := filepath.Abs(*workspacePath)
	if errorValue != nil {
		return runOptions{}, errorValue
	}
	if information, statError := os.Stat(absoluteWorkspacePath); statError != nil || !information.IsDir() {
		return runOptions{}, fmt.Errorf("workspace is not a directory: %s", absoluteWorkspacePath)
	}
	apiKey := ""
	if trimmedPath := strings.TrimSpace(*apiKeyPath); trimmedPath != "" {
		keyBytes, readError := os.ReadFile(trimmedPath)
		if readError != nil {
			return runOptions{}, fmt.Errorf("read model endpoint api key: %w", readError)
		}
		apiKey = strings.TrimSpace(string(keyBytes))
	}
	if strings.TrimSpace(*endpoint) == "" {
		return runOptions{}, fmt.Errorf("a model is required: pass --llm-endpoint")
	}
	return runOptions{
		WorkspacePath: absoluteWorkspacePath,
		Task:          taskText,
		ModelName:     strings.TrimSpace(*modelName),
		Endpoint:      strings.TrimSpace(*endpoint),
		APIKey:        apiKey,
		TurnTimeout:   *turnTimeout,
		ResultPath:    strings.TrimSpace(*resultPath),
	}, nil
}

// TaskResult is what a benchmark harness reads: did the agent answer, what did
// it say, and which files did it hand back.
type TaskResult struct {
	Answered    bool     `json:"answered"`
	Status      string   `json:"status"`
	Reply       string   `json:"reply"`
	Notice      string   `json:"notice,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
	Workspace   string   `json:"workspace"`
}

func runTask(options runOptions) (TaskResult, error) {
	languageModel := openaicompatible.NewProvider(options.Endpoint, options.APIKey, modelNameOrDefault(options.ModelName))

	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()

	agentKernel := loop.NewAgentKernel(taskRunService, taskStepService)
	agentKernel.UseTaskArtifactService(taskArtifactService)
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseTaskTierLanguageModels(agentcontract.TaskTierLanguageModels{
		Max:    languageModel,
		XHigh:  languageModel,
		High:   languageModel,
		Medium: languageModel,
		Low:    languageModel,
		XLow:   languageModel,
	})
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true, DefaultTaskLevel: agentcontract.TaskLevelLow})

	terminalService := security.NewShellService(config.TerminalConfiguration{
		Mode:                  "native",
		WorkspaceRootPath:     options.WorkspacePath,
		TimeoutSecond:         600,
		OutputMaxBytes:        32768,
		SessionMaxCount:       2,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
	})

	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(options.WorkspacePath)
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseWorkspaceActorFactory(security.NewDirectWorkspaceActorFactory(terminalService))
	// The requester identity has to be present or every tool call is refused for
	// a missing actor. It also decides where a bare filename lands, so a
	// standalone run points that person's home at the workspace itself.
	toolSet := toolCatalogBuilder.BuildToolSet(agentruntime.ToolCatalogRequest{
		Prompt:            options.Task,
		RequesterPersonID: defaultRequesterPersonID,
	})

	responseContext, cancel := context.WithTimeout(context.Background(), options.TurnTimeout)
	defer cancel()
	turnResult, errorValue := agentKernel.RunTurn(responseContext, agentcontract.AgentTurnRequest{
		ConversationID:    "bluecollar",
		Prompt:            options.Task,
		RequesterPersonID: defaultRequesterPersonID,
		ToolSet:           toolSet,
		WorkspaceRootPath: options.WorkspacePath,
		InstructionPrompt: workspaceInstruction(options.WorkspacePath),
		// A standalone harness works in the directory it was handed. The
		// appliance layout - private/people/<id> - belongs to a deployment that
		// keeps people apart, and recreating it here buries results where the
		// caller never looks.
		WorkspaceDefaultPath: options.WorkspacePath,
		TurnStartedAt:        time.Now(),
	})
	if errorValue != nil {
		return TaskResult{Workspace: options.WorkspacePath}, errorValue
	}
	return taskResultFromTurn(turnResult, options.WorkspacePath), nil
}

func taskResultFromTurn(turnResult agentcontract.AgentTurnResult, workspacePath string) TaskResult {
	attachments := make([]string, 0, len(turnResult.Attachments))
	for _, attachment := range turnResult.Attachments {
		attachments = append(attachments, attachment.DevicePath)
	}
	reply := strings.TrimSpace(turnResult.FinishMessage)
	return TaskResult{
		Answered:    reply != "",
		Status:      string(turnResult.TaskRun.Status),
		Reply:       reply,
		Notice:      strings.TrimSpace(turnResult.UserNotice),
		Attachments: attachments,
		Workspace:   workspacePath,
	}
}

func modelNameOrDefault(modelName string) string {
	if strings.TrimSpace(modelName) != "" {
		return modelName
	}
	return llm.DefaultModelTierNames().Low
}

// workspaceInstruction tells the agent what a harness run means: the directory it
// was handed is the working directory. Tool descriptions teach an appliance
// convention - a person's home under private/people, artifacts under documents -
// which buries a benchmark's answer where its checker never looks.
func workspaceInstruction(workspacePath string) string {
	return strings.Join([]string{
		"You are running as a standalone agent in a single working directory: " + workspacePath + ".",
		"That directory is the entire workspace. Read and write files there with paths relative to it, for example ./notes.txt.",
		"Never create or write into private/, people/, documents/, or any home-like subdirectory, and never use ~ paths.",
		"Run commands from that directory. When the task names an output file, create it at that exact relative path.",
	}, "\n")
}
