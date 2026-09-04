package tui

import (
	"context"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/enrollment"
)

type setupFieldID int

const (
	setupFieldDisplayName setupFieldID = iota
	setupFieldEmail
	setupFieldWorkspaceRootPath
	setupFieldDatabaseConnectionString
	setupFieldModelEndpointURL
	setupFieldModelName
	setupFieldModelAPIKey
	setupFieldEmbeddingModelName
	setupFieldHarness
	setupFieldMode
)

var setupFieldOrder = []setupFieldID{
	setupFieldDisplayName,
	setupFieldEmail,
	setupFieldWorkspaceRootPath,
	setupFieldDatabaseConnectionString,
	setupFieldModelEndpointURL,
	setupFieldModelName,
	setupFieldModelAPIKey,
	setupFieldEmbeddingModelName,
	setupFieldHarness,
	setupFieldMode,
}

type SetupModel struct {
	home             enrollment.Home
	answers          enrollment.Answers
	availableHarness []enrollment.HarnessChoice
	harnessIndex     int
	cursor           int
	failureNotice    string
	isComplete       bool
	checkResults     []enrollment.CheckResult
	isChecking       bool
	width            int
	height           int
}

func NewSetupModel(home enrollment.Home) SetupModel {
	availableHarness := enrollment.AvailableHarnesses()
	answers := enrollment.SuggestedAnswers(home)
	return SetupModel{
		home:             home,
		answers:          answers,
		availableHarness: availableHarness,
		harnessIndex:     indexOfHarness(availableHarness, answers.Harness.Name),
	}
}

func indexOfHarness(availableHarness []enrollment.HarnessChoice, harnessName string) int {
	for harnessIndex, candidate := range availableHarness {
		if candidate.Name == harnessName {
			return harnessIndex
		}
	}
	return 0
}

func (setupModel SetupModel) fieldLabel(fieldID setupFieldID) string {
	switch fieldID {
	case setupFieldDisplayName:
		return "Your name"
	case setupFieldEmail:
		return "Your email"
	case setupFieldWorkspaceRootPath:
		return "Workspace"
	case setupFieldDatabaseConnectionString:
		return "Postgres"
	case setupFieldModelEndpointURL:
		return "Model endpoint"
	case setupFieldModelName:
		return "Model name"
	case setupFieldModelAPIKey:
		return "API key"
	case setupFieldEmbeddingModelName:
		return "Embedding model"
	case setupFieldHarness:
		return "Harness"
	case setupFieldMode:
		return "Mode"
	}
	return ""
}

func (setupModel SetupModel) fieldValue(fieldID setupFieldID) string {
	switch fieldID {
	case setupFieldDisplayName:
		return setupModel.answers.DisplayName
	case setupFieldEmail:
		return setupModel.answers.Email
	case setupFieldWorkspaceRootPath:
		return setupModel.answers.WorkspaceRootPath
	case setupFieldDatabaseConnectionString:
		return setupModel.answers.DatabaseConnectionString
	case setupFieldModelEndpointURL:
		return setupModel.answers.LanguageModel.EndpointURL
	case setupFieldModelName:
		return setupModel.answers.LanguageModel.ModelName
	case setupFieldModelAPIKey:
		return maskedSecret(setupModel.answers.LanguageModel.APIKey)
	case setupFieldEmbeddingModelName:
		return setupModel.answers.LanguageModel.EmbeddingModelName
	case setupFieldHarness:
		return setupModel.selectedHarnessLabel()
	case setupFieldMode:
		return string(setupModel.answers.Mode)
	}
	return ""
}

func (setupModel SetupModel) selectedHarnessLabel() string {
	if setupModel.harnessIndex < 0 || setupModel.harnessIndex >= len(setupModel.availableHarness) {
		return ""
	}
	selectedHarness := setupModel.availableHarness[setupModel.harnessIndex]
	if strings.TrimSpace(selectedHarness.AgentCommandPath) == "" {
		return selectedHarness.Name
	}
	return selectedHarness.Name + " (" + selectedHarness.AgentCommandPath + ")"
}

func maskedSecret(secret string) string {
	trimmedSecret := strings.TrimSpace(secret)
	if trimmedSecret == "" {
		return ""
	}
	if len(trimmedSecret) <= 6 {
		return strings.Repeat("*", len(trimmedSecret))
	}
	return trimmedSecret[:3] + strings.Repeat("*", len(trimmedSecret)-6) + trimmedSecret[len(trimmedSecret)-3:]
}

func (setupModel *SetupModel) appendToSelectedField(text string) {
	setupModel.setSelectedField(setupModel.rawSelectedFieldValue() + text)
}

func (setupModel *SetupModel) deleteFromSelectedField() {
	rawValue := setupModel.rawSelectedFieldValue()
	if rawValue == "" {
		return
	}
	setupModel.setSelectedField(rawValue[:len(rawValue)-1])
}

func (setupModel SetupModel) rawSelectedFieldValue() string {
	switch setupFieldOrder[setupModel.cursor] {
	case setupFieldDisplayName:
		return setupModel.answers.DisplayName
	case setupFieldEmail:
		return setupModel.answers.Email
	case setupFieldWorkspaceRootPath:
		return setupModel.answers.WorkspaceRootPath
	case setupFieldDatabaseConnectionString:
		return setupModel.answers.DatabaseConnectionString
	case setupFieldModelEndpointURL:
		return setupModel.answers.LanguageModel.EndpointURL
	case setupFieldModelName:
		return setupModel.answers.LanguageModel.ModelName
	case setupFieldModelAPIKey:
		return setupModel.answers.LanguageModel.APIKey
	case setupFieldEmbeddingModelName:
		return setupModel.answers.LanguageModel.EmbeddingModelName
	}
	return ""
}

func (setupModel *SetupModel) setSelectedField(value string) {
	switch setupFieldOrder[setupModel.cursor] {
	case setupFieldDisplayName:
		setupModel.answers.DisplayName = value
	case setupFieldEmail:
		setupModel.answers.Email = value
	case setupFieldWorkspaceRootPath:
		setupModel.answers.WorkspaceRootPath = value
	case setupFieldDatabaseConnectionString:
		setupModel.answers.DatabaseConnectionString = value
	case setupFieldModelEndpointURL:
		setupModel.answers.LanguageModel.EndpointURL = value
	case setupFieldModelName:
		setupModel.answers.LanguageModel.ModelName = value
	case setupFieldModelAPIKey:
		setupModel.answers.LanguageModel.APIKey = value
	case setupFieldEmbeddingModelName:
		setupModel.answers.LanguageModel.EmbeddingModelName = value
	}
}

func (setupModel *SetupModel) cycleSelectedChoice() {
	switch setupFieldOrder[setupModel.cursor] {
	case setupFieldHarness:
		if len(setupModel.availableHarness) == 0 {
			return
		}
		setupModel.harnessIndex = (setupModel.harnessIndex + 1) % len(setupModel.availableHarness)
		setupModel.answers.Harness = setupModel.availableHarness[setupModel.harnessIndex]
	case setupFieldMode:
		if setupModel.answers.Mode == enrollment.RunModeHost {
			setupModel.answers.Mode = enrollment.RunModeGuest
			return
		}
		setupModel.answers.Mode = enrollment.RunModeHost
	}
}

func (setupModel *SetupModel) RunPreflight() {
	setupModel.checkResults = enrollment.Preflight(context.Background(), setupModel.home, setupModel.answers)
	setupModel.isChecking = false
}

func (setupModel SetupModel) CheckResults() []enrollment.CheckResult {
	return setupModel.checkResults
}

func (setupModel *SetupModel) Finish() error {
	setupModel.RunPreflight()
	if !enrollment.IsReadyToStart(setupModel.checkResults) {
		setupModel.failureNotice = "blueclaw cannot start with these answers yet. Each ✗ above says what it needs."
		return errors.New(setupModel.failureNotice)
	}
	enrolled, errorValue := enrollment.NewLocalProvider(setupModel.home, setupModel.answers).Enroll(context.Background())
	if errorValue != nil {
		setupModel.failureNotice = errorValue.Error()
		return errorValue
	}
	if errorValue := enrollment.Materialize(setupModel.home, enrolled); errorValue != nil {
		setupModel.failureNotice = errorValue.Error()
		return errorValue
	}
	setupModel.failureNotice = ""
	setupModel.isComplete = true
	return nil
}
