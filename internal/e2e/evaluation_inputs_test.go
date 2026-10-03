//go:build appliance

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

const (
	capabilityCatalogHint = "point it at the product's generated capability-tools.json, for example internkim's pkg/capabilityprotocol/generated/capability-tools.json"
	skillRootsHint        = "list the product's skill directories, for example internkim's .dependency/internkim-plugin/skills"
	liveConsentHint       = "set it to 1 to accept that the evaluation calls a paid model"
	languageModelHint     = "see \"Live model evaluations\" in DOCS.md for the launch command"
)

func missingEvaluationInputMessage(name string, hint string) string {
	return fmt.Sprintf("the evaluation was requested with -tags llmeval, but %s is not set; %s", name, hint)
}

func exitWithoutEvaluationInput(name string, hint string, plainRunMessage string) {
	if evaluationRequested {
		fmt.Println(missingEvaluationInputMessage(name, hint))
		os.Exit(1)
	}
	fmt.Println(plainRunMessage)
	os.Exit(0)
}

func requireEvaluationInput(t *testing.T, name string, hint string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatal(missingEvaluationInputMessage(name, hint))
	}
	return value
}

func requireAnyEvaluationInput(t *testing.T, names []string, hint string) {
	t.Helper()
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return
		}
	}
	t.Fatal(missingEvaluationInputMessage(strings.Join(names, " or "), hint))
}

func requireLiveEvaluationConsent(t *testing.T) {
	t.Helper()
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Fatal(missingEvaluationInputMessage("BLUECLAW_E2E_LIVE", liveConsentHint))
	}
}

func requireLanguageModelInput(t *testing.T) {
	t.Helper()
	requireAnyEvaluationInput(t, []string{"BLUECLAW_E2E_LLM_ENDPOINT", "BLUECLAW_E2E_LLM_UNIX_SOCKET"}, languageModelHint)
}

func requireDecisionModelInput(t *testing.T) {
	t.Helper()
	requireAnyEvaluationInput(t, []string{"BLUECLAW_DECISION_ENDPOINT", "BLUECLAW_DECISION_UNIX_SOCKET"}, languageModelHint)
}
