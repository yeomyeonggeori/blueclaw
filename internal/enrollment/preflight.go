package enrollment

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

type CheckName string

const (
	CheckDatabase      CheckName = "postgres"
	CheckLanguageModel CheckName = "language model"
	CheckHarness       CheckName = "harness"
	CheckWorkspace     CheckName = "workspace"
)

type CheckResult struct {
	Name     CheckName
	IsReady  bool
	Detail   string
	Guidance string
}

const databaseCheckTimeout = 5 * time.Second

func Preflight(ctx context.Context, home Home, answers Answers) []CheckResult {
	return []CheckResult{
		checkDatabase(ctx, home, answers.DatabaseConnectionString),
		checkLanguageModel(answers.LanguageModel),
		checkHarness(answers.Harness),
		checkWorkspace(answers.WorkspaceRootPath),
	}
}

func checkDatabase(ctx context.Context, home Home, connectionString string) CheckResult {
	if strings.TrimSpace(connectionString) == "" {
		return CheckResult{Name: CheckDatabase, Guidance: "blueclaw keeps every task, event and memory in Postgres, so it needs a connection string."}
	}
	managedPostgres := NewManagedPostgres(home)
	if connectionString == managedPostgres.ConnectionString() {
		if errorValue := managedPostgres.EnsureRunning(ctx); errorValue != nil {
			return CheckResult{Name: CheckDatabase, Detail: errorValue.Error(), Guidance: "Point this line at a Postgres you already run instead."}
		}
		return CheckResult{Name: CheckDatabase, IsReady: true, Detail: "managed by blueclaw at " + managedPostgres.DirectoryPath()}
	}
	connectContext, cancel := context.WithTimeout(ctx, databaseCheckTimeout)
	defer cancel()
	database, errorValue := postgres.OpenDatabase(connectContext, connectionString)
	if errorValue != nil {
		return CheckResult{Name: CheckDatabase, Detail: errorValue.Error(), Guidance: "Start a Postgres and create a database blueclaw can reach, then correct this line."}
	}
	database.Close()
	return CheckResult{Name: CheckDatabase, IsReady: true}
}

func checkLanguageModel(access LanguageModelAccess) CheckResult {
	endpointURL := strings.TrimSpace(access.EndpointURL)
	if endpointURL == "" {
		return CheckResult{Name: CheckLanguageModel, Guidance: "Give blueclaw a model endpoint to ask, and a key for it if that endpoint wants one."}
	}
	modelName := strings.TrimSpace(access.ModelName)
	if modelName == "" {
		return CheckResult{Name: CheckLanguageModel, Guidance: "Name the model this endpoint serves, the way the endpoint spells it."}
	}
	return CheckResult{Name: CheckLanguageModel, IsReady: true, Detail: modelName + " at " + endpointURL}
}

func checkHarness(harness HarnessChoice) CheckResult {
	if strings.TrimSpace(harness.Name) == "" || harness.Name == "bluecollar" {
		return CheckResult{Name: CheckHarness, IsReady: true, Detail: "bluecollar (built in)"}
	}
	commandPath := strings.TrimSpace(harness.AgentCommandPath)
	if commandPath == "" {
		return CheckResult{Name: CheckHarness, Guidance: "This harness runs a command, so blueclaw needs its path."}
	}
	if _, errorValue := os.Stat(commandPath); errorValue != nil {
		return CheckResult{Name: CheckHarness, Detail: errorValue.Error(), Guidance: "Install that agent, or choose the built-in bluecollar harness."}
	}
	return CheckResult{Name: CheckHarness, IsReady: true, Detail: commandPath}
}

func checkWorkspace(workspaceRootPath string) CheckResult {
	if strings.TrimSpace(workspaceRootPath) == "" {
		return CheckResult{Name: CheckWorkspace, Guidance: "The agent's work has to live somewhere."}
	}
	if errorValue := os.MkdirAll(workspaceRootPath, 0o755); errorValue != nil {
		return CheckResult{Name: CheckWorkspace, Detail: errorValue.Error(), Guidance: "Choose a directory this account may write to."}
	}
	return CheckResult{Name: CheckWorkspace, IsReady: true, Detail: workspaceRootPath}
}

func IsReadyToStart(checkResults []CheckResult) bool {
	for _, checkResult := range checkResults {
		if !checkResult.IsReady {
			return false
		}
	}
	return true
}
