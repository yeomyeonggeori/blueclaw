package tui

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/yeomyeonggeori/blueclaw/internal/enrollment"
)

func setupModelFixture(t *testing.T) SetupModel {
	t.Helper()
	return NewSetupModel(enrollment.Home{DirectoryPath: filepath.Join(t.TempDir(), "blueclaw")})
}

func typeText(setupModel SetupModel, text string) SetupModel {
	for _, character := range text {
		updated, _ := setupModel.Update(tea.KeyPressMsg{Code: character, Text: string(character)})
		setupModel = updated.(SetupModel)
	}
	return setupModel
}

func pressKey(setupModel SetupModel, keyName string) SetupModel {
	updated, _ := setupModel.Update(keyPressForName(keyName))
	return updated.(SetupModel)
}

func keyPressForName(keyName string) tea.KeyPressMsg {
	switch keyName {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	return tea.KeyPressMsg{Code: tea.KeySpace}
}

func reachableLanguageModel() enrollment.LanguageModelAccess {
	return enrollment.LanguageModelAccess{
		EndpointURL: "https://models.example.com/v1",
		ModelName:   "example-model-large",
		APIKey:      "test-key",
	}
}

func TestSetupWillNotFinishAnInstallThatCannotStart(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.answers.LanguageModel = reachableLanguageModel()
	setupModel.answers.DatabaseConnectionString = "postgres://nobody@127.0.0.1:1/blueclaw?sslmode=disable&connect_timeout=1"

	if errorValue := (&setupModel).Finish(); errorValue == nil {
		t.Fatal("expected setup to refuse while a dependency is unreachable, because the alternative is an install that only looks finished")
	}
	if setupModel.home.IsEnrolled() {
		t.Fatal("expected nothing to be written when the checks did not pass")
	}
	if !hasFailedCheck(setupModel, enrollment.CheckDatabase) {
		t.Fatalf("expected the unreachable database to be named, got %+v", setupModel.CheckResults())
	}
}

func hasFailedCheck(setupModel SetupModel, checkName enrollment.CheckName) bool {
	for _, checkResult := range setupModel.CheckResults() {
		if checkResult.Name == checkName && !checkResult.IsReady {
			return true
		}
	}
	return false
}

func TestSetupNamesEveryDependencyItCouldNotReach(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.answers.LanguageModel = enrollment.LanguageModelAccess{}
	setupModel.answers.Harness = enrollment.HarnessChoice{Name: "claude-code", AgentCommandPath: "/nonexistent/claude"}

	(&setupModel).RunPreflight()

	if !hasFailedCheck(setupModel, enrollment.CheckLanguageModel) {
		t.Fatal("expected a missing model path to be reported")
	}
	if !hasFailedCheck(setupModel, enrollment.CheckHarness) {
		t.Fatal("expected a harness command that is not installed to be reported")
	}
}

func TestSetupRefusesToFinishWhenTheEndpointNamesNoModel(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.answers.LanguageModel = reachableLanguageModel()
	setupModel.answers.LanguageModel.ModelName = ""

	(&setupModel).RunPreflight()

	if !hasFailedCheck(setupModel, enrollment.CheckLanguageModel) {
		t.Fatalf("expected an endpoint with no model named on it to be reported, got %+v", setupModel.CheckResults())
	}
}

func TestSetupRefusesToFinishWithNoWayToReachAModel(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.answers.LanguageModel = enrollment.LanguageModelAccess{}

	if errorValue := (&setupModel).Finish(); errorValue == nil {
		t.Fatal("expected setup to refuse, because an install that cannot reach a model fails at the first turn instead")
	}
	if setupModel.failureNotice == "" {
		t.Fatal("expected the person setting this up to be told why")
	}
	if setupModel.home.IsEnrolled() {
		t.Fatal("expected nothing to be written when setup could not finish")
	}
}

func TestTypingEditsTheSelectedFieldOnly(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.answers.DisplayName = ""
	originalEmail := setupModel.answers.Email

	setupModel = typeText(setupModel, "lee")

	if setupModel.answers.DisplayName != "lee" {
		t.Fatalf("expected typing to edit the selected field, got %q", setupModel.answers.DisplayName)
	}
	if setupModel.answers.Email != originalEmail {
		t.Fatalf("expected other fields to be left alone, got %q", setupModel.answers.Email)
	}
}

func TestTheKeyIsNeverShownBackInFull(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.answers.LanguageModel.APIKey = "endpoint-secret-value"

	shownValue := setupModel.fieldValue(setupFieldModelAPIKey)

	if shownValue == setupModel.answers.LanguageModel.APIKey {
		t.Fatal("expected the key to be masked on screen, because setup runs where people can see the terminal")
	}
	if shownValue == "" {
		t.Fatal("expected some evidence the key is set, so it is not mistaken for missing")
	}
}
