package enrollment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/harnessselection"
)

type Answers struct {
	DisplayName              string
	Email                    string
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
		WorkspaceRootPath:        home.WorkspaceRootPath(),
		DatabaseConnectionString: detectedDatabaseConnectionString(home),
		LanguageModel:            detectedLanguageModelAccess(),
		Harness:                  detectedHarness(),
	}
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
	installed := installedCommandHarnesses()
	if len(installed) == 0 {
		return HarnessChoice{}
	}
	return installed[0]
}

func AvailableHarnesses() []HarnessChoice {
	return append([]HarnessChoice{{Name: harnessselection.BundledACPHarnessName}}, installedCommandHarnesses()...)
}

func installedCommandHarnesses() []HarnessChoice {
	installed := []HarnessChoice{}
	for _, commandHarness := range harnessselection.CommandHarnesses() {
		if commandPath, errorValue := exec.LookPath(commandHarness.ExecutableName); errorValue == nil {
			installed = append(installed, HarnessChoice{Name: commandHarness.Name, AgentCommandPath: commandPath})
		}
	}
	return installed
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
