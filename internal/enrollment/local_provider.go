package enrollment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"os/user"
	"strings"
)

type Answers struct {
	DisplayName              string
	Email                    string
	Mode                     RunMode
	WorkspaceRootPath        string
	DatabaseConnectionString string
	LanguageModel            LanguageModelAccess
	Harness                  HarnessChoice
}

type LocalProvider struct {
	home    Home
	answers Answers
}

func NewLocalProvider(home Home, answers Answers) LocalProvider {
	return LocalProvider{home: home, answers: answers}
}

func (provider LocalProvider) Enroll(context.Context) (Enrollment, error) {
	answers := provider.answers
	enrollment := Enrollment{
		TenantID:                 newIdentifier(),
		Mode:                     firstNonEmptyMode(answers.Mode, RunModeHost),
		Operator:                 Person{PersonID: newIdentifier(), DisplayName: answers.DisplayName, Email: answers.Email},
		WorkspaceRootPath:        firstNonEmptyString(answers.WorkspaceRootPath, provider.home.WorkspaceRootPath()),
		DatabaseConnectionString: answers.DatabaseConnectionString,
		LanguageModel:            answers.LanguageModel,
		Harness:                  answers.Harness,
	}
	if strings.TrimSpace(enrollment.Operator.DisplayName) == "" {
		enrollment.Operator.DisplayName = currentAccountName()
	}
	if errorValue := enrollment.Validate(); errorValue != nil {
		return Enrollment{}, errorValue
	}
	return enrollment, nil
}

func SuggestedAnswers(home Home) Answers {
	accountName := currentAccountName()
	return Answers{
		DisplayName:              accountName,
		Email:                    accountName + "@localhost",
		Mode:                     RunModeHost,
		WorkspaceRootPath:        home.WorkspaceRootPath(),
		DatabaseConnectionString: detectedDatabaseConnectionString(home),
		LanguageModel:            detectedLanguageModelAccess(),
		Harness:                  detectedHarness(),
	}
}

var harnessCommandNames = []struct {
	harnessName string
	commandName string
}{
	{harnessName: "claude-code", commandName: "claude"},
	{harnessName: "codex", commandName: "codex"},
}

func detectedLanguageModelAccess() LanguageModelAccess {
	return LanguageModelAccess{
		EndpointURL:        strings.TrimSpace(os.Getenv("BLUECLAW_MODEL_ENDPOINT")),
		ModelName:          strings.TrimSpace(os.Getenv("BLUECLAW_MODEL_NAME")),
		APIKey:             strings.TrimSpace(os.Getenv("BLUECLAW_MODEL_API_KEY")),
		EmbeddingModelName: strings.TrimSpace(os.Getenv("BLUECLAW_EMBEDDING_MODEL_NAME")),
	}
}

func detectedHarness() HarnessChoice {
	for _, candidate := range harnessCommandNames {
		commandPath, errorValue := exec.LookPath(candidate.commandName)
		if errorValue == nil {
			return HarnessChoice{Name: candidate.harnessName, AgentCommandPath: commandPath}
		}
	}
	return HarnessChoice{}
}

func AvailableHarnesses() []HarnessChoice {
	available := []HarnessChoice{{Name: "bluecollar"}}
	for _, candidate := range harnessCommandNames {
		if commandPath, errorValue := exec.LookPath(candidate.commandName); errorValue == nil {
			available = append(available, HarnessChoice{Name: candidate.harnessName, AgentCommandPath: commandPath})
		}
	}
	return available
}

func detectedDatabaseConnectionString(home Home) string {
	if configuredURL := strings.TrimSpace(os.Getenv("DATABASE_URL")); configuredURL != "" {
		return configuredURL
	}
	return NewManagedPostgres(home).ConnectionString()
}

func currentAccountName() string {
	currentUser, errorValue := user.Current()
	if errorValue != nil || strings.TrimSpace(currentUser.Username) == "" {
		return "blueclaw"
	}
	return currentUser.Username
}

func newIdentifier() string {
	identifierBytes := make([]byte, 16)
	if _, errorValue := rand.Read(identifierBytes); errorValue != nil {
		return ""
	}
	return hex.EncodeToString(identifierBytes)
}

func firstNonEmptyString(candidates ...string) string {
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmptyMode(candidates ...RunMode) RunMode {
	for _, candidate := range candidates {
		if strings.TrimSpace(string(candidate)) != "" {
			return candidate
		}
	}
	return RunModeHost
}
