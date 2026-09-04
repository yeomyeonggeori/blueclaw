package enrollment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
)

func homeFixture(t *testing.T) Home {
	t.Helper()
	return Home{DirectoryPath: filepath.Join(t.TempDir(), "blueclaw")}
}

func completeAnswers(home Home) Answers {
	return Answers{
		DisplayName:              "정예시",
		Email:                    "lee@example.com",
		Mode:                     RunModeHost,
		WorkspaceRootPath:        home.WorkspaceRootPath(),
		DatabaseConnectionString: "postgres://blueclaw@127.0.0.1:5432/blueclaw?sslmode=disable",
		LanguageModel: LanguageModelAccess{
			EndpointURL:        "https://models.example.com/v1",
			ModelName:          "example-model-large",
			APIKey:             "test-key",
			EmbeddingModelName: "example-embedding-small",
		},
		Harness: HarnessChoice{Name: "claude-code", AgentCommandPath: "/usr/local/bin/claude"},
	}
}

func TestAFreshInstallIsNotEnrolledUntilItHasBothDocuments(t *testing.T) {
	home := homeFixture(t)
	if home.IsEnrolled() {
		t.Fatal("expected a fresh install to know it has nothing yet")
	}

	enrolled, errorValue := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	if errorValue != nil {
		t.Fatalf("expected a single person to be enough to enroll: %v", errorValue)
	}
	if errorValue := Materialize(home, enrolled); errorValue != nil {
		t.Fatalf("expected the enrollment to be written: %v", errorValue)
	}
	if !home.IsEnrolled() {
		t.Fatal("expected the install to be enrolled once both documents exist")
	}
}

func TestTheWrittenConfigurationIsTheOneTheRuntimeLoads(t *testing.T) {
	home := homeFixture(t)
	enrolled, _ := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	if errorValue := Materialize(home, enrolled); errorValue != nil {
		t.Fatalf("expected the enrollment to be written: %v", errorValue)
	}

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(home.RuntimeConfigurationPath())
	if errorValue != nil {
		t.Fatalf("expected the runtime to load what onboarding wrote, because otherwise setup only looks finished: %v", errorValue)
	}
	if runtimeConfiguration.Terminal.WorkspaceRootPath != home.WorkspaceRootPath() {
		t.Fatalf("expected the workspace to be the one enrolled, got %q", runtimeConfiguration.Terminal.WorkspaceRootPath)
	}
	if runtimeConfiguration.Agent.Harness.Name != "claude-code" {
		t.Fatalf("expected the chosen harness to survive setup, got %q", runtimeConfiguration.Agent.Harness.Name)
	}
}

func TestEveryTierReachesTheEndpointTheOperatorGave(t *testing.T) {
	home := homeFixture(t)
	answers := completeAnswers(home)
	enrolled, _ := NewLocalProvider(home, answers).Enroll(context.Background())
	if errorValue := Materialize(home, enrolled); errorValue != nil {
		t.Fatalf("expected the enrollment to be written: %v", errorValue)
	}

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(home.RuntimeConfigurationPath())
	if errorValue != nil {
		t.Fatalf("expected the runtime to load what onboarding wrote: %v", errorValue)
	}
	for _, modelTierName := range llm.ModelTiers {
		rungs := runtimeConfiguration.LanguageModel.Tiers[modelTierName]
		if len(rungs) != 1 {
			t.Fatalf("expected the %s tier to carry the one endpoint setup collected, got %v", modelTierName, rungs)
		}
		if rungs[0].Endpoint != answers.LanguageModel.EndpointURL || rungs[0].Model != answers.LanguageModel.ModelName {
			t.Fatalf("expected the %s tier to reach %q at %q, got %+v", modelTierName, answers.LanguageModel.ModelName, answers.LanguageModel.EndpointURL, rungs[0])
		}
		if rungs[0].APIKeyPath != home.ModelAPIKeyPath() {
			t.Fatalf("expected the %s tier to read the key setup wrote, got %q", modelTierName, rungs[0].APIKeyPath)
		}
	}
	if runtimeConfiguration.LanguageModel.Embedding.Model != answers.LanguageModel.EmbeddingModelName {
		t.Fatalf("expected the embedding model to survive setup, got %+v", runtimeConfiguration.LanguageModel.Embedding)
	}

	storedKey, errorValue := os.ReadFile(home.ModelAPIKeyPath())
	if errorValue != nil {
		t.Fatalf("expected the key to be written where the configuration points: %v", errorValue)
	}
	if strings.TrimSpace(string(storedKey)) != answers.LanguageModel.APIKey {
		t.Fatalf("expected the key file to hold the key that was given, got %q", strings.TrimSpace(string(storedKey)))
	}
}

func TestAnEnrollmentWithoutAnEmbeddingModelWritesNone(t *testing.T) {
	home := homeFixture(t)
	answers := completeAnswers(home)
	answers.LanguageModel.EmbeddingModelName = ""
	enrolled, _ := NewLocalProvider(home, answers).Enroll(context.Background())
	if errorValue := Materialize(home, enrolled); errorValue != nil {
		t.Fatalf("expected the enrollment to be written: %v", errorValue)
	}

	runtimeConfiguration, _ := config.LoadRuntimeConfiguration(home.RuntimeConfigurationPath())
	if runtimeConfiguration.LanguageModel.Embedding.Model != "" {
		t.Fatalf("expected no embedding endpoint to be invented, got %+v", runtimeConfiguration.LanguageModel.Embedding)
	}
}

func TestTheOperatorBecomesAPersonTheAgentCanRunAs(t *testing.T) {
	home := homeFixture(t)
	enrolled, _ := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	Materialize(home, enrolled)

	policyDocument := map[string]any{}
	policyBytes, errorValue := os.ReadFile(home.PolicyPath())
	if errorValue != nil {
		t.Fatalf("expected a policy to be written: %v", errorValue)
	}
	if errorValue := json.Unmarshal(policyBytes, &policyDocument); errorValue != nil {
		t.Fatalf("expected the policy to be readable: %v", errorValue)
	}
	people, _ := policyDocument["people"].([]any)
	if len(people) != 1 {
		t.Fatalf("expected the person who set this up to exist, got %v", policyDocument["people"])
	}
	if !strings.Contains(string(policyBytes), "lee@example.com") {
		t.Fatal("expected the operator's email in the policy, because that is how a message is matched to a person")
	}
}

func TestAnEnrollmentWithNoWayToReachAModelIsRefused(t *testing.T) {
	home := homeFixture(t)
	answers := completeAnswers(home)
	answers.LanguageModel = LanguageModelAccess{}

	if _, errorValue := NewLocalProvider(home, answers).Enroll(context.Background()); errorValue == nil {
		t.Fatal("expected setup to refuse an install that cannot reach a model, rather than failing later at the first turn")
	}
}

func TestAnEndpointWithNoModelNamedOnItIsRefused(t *testing.T) {
	home := homeFixture(t)
	answers := completeAnswers(home)
	answers.LanguageModel.ModelName = ""

	if _, errorValue := NewLocalProvider(home, answers).Enroll(context.Background()); errorValue == nil {
		t.Fatal("expected setup to refuse an endpoint with no model named on it, because every request has to name one")
	}
}

func TestHomeFollowsTheEnvironmentSoOneMachineCanHoldSeveralInstalls(t *testing.T) {
	t.Setenv("BLUECLAW_HOME", "/tmp/blueclaw-test-home")
	if home := ResolveHome(); home.DirectoryPath != "/tmp/blueclaw-test-home" {
		t.Fatalf("expected the configured home to win, got %q", home.DirectoryPath)
	}
}
