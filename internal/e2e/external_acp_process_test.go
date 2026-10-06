//go:build appliance

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/acpharness"
	"github.com/yeomyeonggeori/blueclaw/internal/bluecollaracp"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const (
	bluecollarACPSourcePath = "../../.dependency/bluecollar/cmd/bluecollar-acp"
	scriptedModelName       = "scripted"
)

type scenarioOutcome struct {
	Failure string
	Turns   []turnOutcome
}

type turnOutcome struct {
	TaskStatus         string
	FinishMessage      string
	EventNameCounts    map[string]int
	DidReply           bool
	FailureReason      string
	AttachmentFileName []string
}

func TestEveryBuiltinScenarioGivesTheSameOutcomeOverAnExternalACPProcess_EventOrderAndReferenceIDsMayDiffer(t *testing.T) {
	defaultFactory := virtualSessionAgentHarnessFactory
	externalFactory := externalProcessFactory(t, buildBluecollarACP(t))
	defer UseAgentHarnessFactory(defaultFactory)

	for _, scenarioName := range scriptedScenarioNames(t) {
		t.Run(scenarioName, func(t *testing.T) {
			inProcess := runScenarioUnder(t, defaultFactory, scenarioName)
			external := runScenarioUnder(t, externalFactory, scenarioName)
			for _, difference := range outcomeDifferences(inProcess, external) {
				t.Errorf("outcome differs over an external ACP process (event order and reference ids are not compared): %s", difference)
			}
		})
	}
}

func scriptedScenarioNames(t *testing.T) []string {
	t.Helper()
	scriptedNames := []string{}
	for _, scenarioName := range BuiltinScenarioNames() {
		scenario, errorValue := BuiltinScenario(scenarioName, t.TempDir())
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !scenario.NeedsLiveLanguageModel() {
			scriptedNames = append(scriptedNames, scenarioName)
		}
	}
	return scriptedNames
}

func runScenarioUnder(t *testing.T, factory harnessdriver.Factory, scenarioName string) scenarioOutcome {
	t.Helper()
	UseAgentHarnessFactory(factory)
	scenario, errorValue := BuiltinScenario(scenarioName, t.TempDir())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	result, errorValue := RunVirtualSession(context.Background(), scenario)
	return outcomeOf(result, errorValue)
}

func outcomeOf(result VirtualSessionResult, errorValue error) scenarioOutcome {
	outcome := scenarioOutcome{}
	if errorValue != nil {
		outcome.Failure = errorValue.Error()
	}
	for _, turnResult := range result.TurnResults {
		outcome.Turns = append(outcome.Turns, turnOutcomeOf(turnResult))
	}
	return outcome
}

func turnOutcomeOf(turnResult VirtualTurnResult) turnOutcome {
	eventNameCounts := map[string]int{}
	for _, event := range turnResult.Events {
		eventNameCounts[event.Name]++
	}
	attachmentFileNames := []string{}
	for _, attachment := range turnResult.Attachments {
		attachmentFileNames = append(attachmentFileNames, filepath.Base(attachment.Filename))
	}
	sort.Strings(attachmentFileNames)
	return turnOutcome{
		TaskStatus:         string(turnResult.TaskStatus),
		FinishMessage:      withoutReplyReference(turnResult.FinishMessage),
		EventNameCounts:    eventNameCounts,
		DidReply:           turnResult.DidReply,
		FailureReason:      turnResult.FailureReason,
		AttachmentFileName: attachmentFileNames,
	}
}

var replyReferencePattern = regexp.MustCompile("`[0-9a-f]{6}`$")

func withoutReplyReference(finishMessage string) string {
	return replyReferencePattern.ReplaceAllString(finishMessage, "`reference`")
}

func outcomeDifferences(inProcess scenarioOutcome, external scenarioOutcome) []string {
	differences := []string{}
	if inProcess.Failure != external.Failure {
		differences = append(differences, fmt.Sprintf("failure: in process %q, external %q", inProcess.Failure, external.Failure))
	}
	if len(inProcess.Turns) != len(external.Turns) {
		return append(differences, fmt.Sprintf("turn count: in process %d, external %d", len(inProcess.Turns), len(external.Turns)))
	}
	for turnIndex := range inProcess.Turns {
		differences = append(differences, turnDifferences(turnIndex, inProcess.Turns[turnIndex], external.Turns[turnIndex])...)
	}
	return differences
}

func turnDifferences(turnIndex int, inProcess turnOutcome, external turnOutcome) []string {
	differences := []string{}
	for _, field := range []struct{ name, inProcess, external string }{
		{"task status", inProcess.TaskStatus, external.TaskStatus},
		{"final reply", inProcess.FinishMessage, external.FinishMessage},
		{"failure reason", inProcess.FailureReason, external.FailureReason},
		{"replied", fmt.Sprint(inProcess.DidReply), fmt.Sprint(external.DidReply)},
		{"attachments", fmt.Sprint(inProcess.AttachmentFileName), fmt.Sprint(external.AttachmentFileName)},
	} {
		if field.inProcess != field.external {
			differences = append(differences, fmt.Sprintf("turn %d %s: in process %q, external %q", turnIndex, field.name, field.inProcess, field.external))
		}
	}
	if eventDifference := eventCountDifference(inProcess.EventNameCounts, external.EventNameCounts); eventDifference != "" {
		differences = append(differences, fmt.Sprintf("turn %d event names: %s", turnIndex, eventDifference))
	}
	return differences
}

func eventCountDifference(inProcess map[string]int, external map[string]int) string {
	names := map[string]bool{}
	for name := range inProcess {
		names[name] = true
	}
	for name := range external {
		names[name] = true
	}
	differing := []string{}
	for name := range names {
		if inProcess[name] != external[name] {
			differing = append(differing, fmt.Sprintf("%s in process %d external %d", name, inProcess[name], external[name]))
		}
	}
	sort.Strings(differing)
	return strings.Join(differing, "; ")
}

func buildBluecollarACP(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "bluecollar-acp")
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	build.Dir = bluecollarACPSourcePath
	if output, errorValue := build.CombinedOutput(); errorValue != nil {
		t.Fatalf("build bluecollar-acp: %v\n%s", errorValue, output)
	}
	return binaryPath
}

func externalProcessFactory(t *testing.T, binaryPath string) harnessdriver.Factory {
	t.Helper()
	processFor := func(dependencies harnessdriver.Dependencies, _ agentcontract.SkillRetriever) acpharness.AgentProcess {
		endpoint := scenarioModelEndpoint{languageModel: dependencies.TaskTierLanguageModels.Low, decisionModel: dependencies.DecisionModel}
		server := httptest.NewServer(endpoint.handler())
		t.Cleanup(server.Close)
		return externalScenarioProcess{binaryPath: binaryPath, serverURL: server.URL, dependencies: dependencies}
	}
	factory, errorValue := bundledACPHarnessFactory(bluecollaracp.NewFactoryOverProcess(processFor))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return factory
}

type externalScenarioProcess struct {
	binaryPath   string
	serverURL    string
	dependencies harnessdriver.Dependencies
}

func (process externalScenarioProcess) Start(ctx context.Context) (io.Writer, io.Reader, func() error, error) {
	request, isPresent := acpharness.TurnRequestFrom(ctx)
	if !isPresent {
		return nil, nil, nil, errors.New("the external agent starts for a turn, and this start carried none")
	}
	bundlePath, errorValue := writeInstructionBundle(process.dependencies.InstructionBundleLoader())
	if errorValue != nil {
		return nil, nil, nil, errorValue
	}
	input, output, wait, errorValue := process.command(bundlePath, request).Start(ctx)
	if errorValue != nil {
		_ = os.Remove(bundlePath)
		return nil, nil, nil, errorValue
	}
	return input, output, func() error {
		defer os.Remove(bundlePath)
		return wait()
	}, nil
}

func (process externalScenarioProcess) command(bundlePath string, request agentcontract.AgentTurnRequest) acpharness.AgentCommand {
	return acpharness.AgentCommand{
		Path: process.binaryPath,
		Arguments: []string{
			"-endpoint", process.serverURL + "/v1",
			"-model", scriptedModelName,
			"-structured-output-only",
			"-instruction-bundle", bundlePath,
			"-host-checked-tools", strings.Join(bluecollaracp.HostCheckedToolNames(request), ","),
			"-pinned-skills", strings.Join(request.PinnedSkillNames, ","),
		},
		Environment: append(os.Environ(), decisionEnvironment(process.serverURL, process.dependencies)...),
	}
}

func writeInstructionBundle(bundle agentcontract.InstructionBundle) (string, error) {
	document, errorValue := json.Marshal(bundle)
	if errorValue != nil {
		return "", errorValue
	}
	file, errorValue := os.CreateTemp("", "instruction-bundle-*.json")
	if errorValue != nil {
		return "", errorValue
	}
	defer file.Close()
	_, errorValue = file.Write(document)
	return file.Name(), errorValue
}

func decisionEnvironment(serverURL string, dependencies harnessdriver.Dependencies) []string {
	if dependencies.DecisionModel == nil {
		return nil
	}
	return []string{
		"BLUECOLLAR_DECISION_ENDPOINT=" + serverURL + "/decisions",
		"BLUECOLLAR_DECISION_API_KEY=scripted",
		"BLUECOLLAR_DECISION_MODEL=" + scriptedModelName,
	}
}
