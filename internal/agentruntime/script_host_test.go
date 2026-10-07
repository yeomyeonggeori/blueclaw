//go:build !nobundledharness

package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type answeringDecisionModel struct {
	mutex    sync.Mutex
	name     string
	requests []model.DecisionRequest
}

func (decisionModel *answeringDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	decisionModel.requests = append(decisionModel.requests, request)
	answers := map[string]model.DecisionAnswer{}
	for name := range request.Questions {
		answers[name] = model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: "source", Probabilities: map[string]float64{"source": 0.9, "claim": 0.1}}
	}
	return model.DecisionResponse{Answers: answers, ModelName: decisionModel.name, Usage: model.Usage{CostUSD: 0.002}}, nil
}

type answeringLanguageModel struct {
	mutex    sync.Mutex
	requests []model.StructuredResponseRequest
}

func (languageModel *answeringLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("unused")
}

func (languageModel *answeringLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.requests = append(languageModel.requests, request)
	return model.StructuredResponse{Content: `{"text":"rewritten"}`, ModelName: "medium", Usage: model.Usage{CostUSD: 0.01}}, nil
}

type scriptHostFixture struct {
	taskFixture
	decisionModel      *answeringDecisionModel
	imageDecisionModel *answeringDecisionModel
	languageModel      *answeringLanguageModel
	server             *httptest.Server
}

func newScriptHostFixture(t *testing.T) scriptHostFixture {
	t.Helper()
	fixture := scriptHostFixture{
		taskFixture:        newTaskFixture(t, companyInfoGetDescriptor()),
		decisionModel:      &answeringDecisionModel{name: "jev"},
		imageDecisionModel: &answeringDecisionModel{name: "clef"},
		languageModel:      &answeringLanguageModel{},
	}
	host := NewScriptHost()
	fixture.server = httptest.NewServer(host.Handler())
	t.Cleanup(fixture.server.Close)
	fixture.builder.UseScriptHost(host, fixture.server.URL)
	fixture.builder.UseScriptModels(fixture.decisionModel, fixture.imageDecisionModel, fixture.languageModel)
	return fixture
}

func (fixture scriptHostFixture) askTheHost(t *testing.T, route string, body string) string {
	t.Helper()
	requestPath := filepath.Join(fixture.homePath(), "request.json")
	writeTestFile(t, requestPath, body)
	return fixture.shellOutput(t, `curl -sS -X POST -H "Authorization: Bearer $SKILL_HOST_TOKEN" --data-binary @`+shellSingleQuoted(requestPath)+` "$SKILL_HOST_URL/`+route+`"`)
}

const choiceQuestionBody = `{"state":{"request":"견적서"},"questions":{"claim0":{"type":"choice","instructions":"Which kind is claim0?","criteria":{"source":"says what the request says","claim":"the request lacks it"}}}}`

func TestACommandAsksTheDecisionModelThroughTheHost(t *testing.T) {
	fixture := newScriptHostFixture(t)

	output := fixture.askTheHost(t, "decide", choiceQuestionBody)

	var response model.DecisionResponse
	if errorValue := json.Unmarshal([]byte(output), &response); errorValue != nil {
		t.Fatalf("the host answered %q", output)
	}
	if response.Answers["claim0"].Choice != "source" || response.ModelName != "jev" {
		t.Fatalf("the host answered %+v", response)
	}
	if len(fixture.decisionModel.requests) != 1 || len(fixture.imageDecisionModel.requests) != 0 {
		t.Fatalf("the decision model was asked %d times and the image model %d", len(fixture.decisionModel.requests), len(fixture.imageDecisionModel.requests))
	}
	asked := fixture.decisionModel.requests[0].Questions["claim0"]
	if asked.Type != model.DecisionQuestionTypeChoice || asked.Instructions != "Which kind is claim0?" {
		t.Fatalf("the question reached the model as %+v", asked)
	}
}

func TestAQuestionAboutAnImageGoesToTheImageModel(t *testing.T) {
	fixture := newScriptHostFixture(t)
	body := `{"state":{"slide":1},"questions":{"visual_defect":{"type":"choice","instructions":"Which defect?","criteria":{"none":"clean","crowded":"too full"}}},"images":[{"mediaType":"image/png","data":"UE5H"}]}`

	fixture.askTheHost(t, "decide", body)

	if len(fixture.imageDecisionModel.requests) != 1 || len(fixture.decisionModel.requests) != 0 {
		t.Fatalf("the image model was asked %d times and the decision model %d", len(fixture.imageDecisionModel.requests), len(fixture.decisionModel.requests))
	}
	image := fixture.imageDecisionModel.requests[0].Images[0]
	if image.MediaType != "image/png" || string(image.Data) != "PNG" {
		t.Fatalf("the image reached the model as %s %q", image.MediaType, image.Data)
	}
}

func TestACommandAsksForAnAnswerInItsOwnSchema(t *testing.T) {
	fixture := newScriptHostFixture(t)
	body := `{"system":"You rewrite one unit.","prompt":"Unit: 최고의 서비스","images":[{"mediaType":"image/png","data":"UE5H"}],"schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}}`

	output := fixture.askTheHost(t, "generate", body)

	var response scriptGenerateResponse
	if errorValue := json.Unmarshal([]byte(output), &response); errorValue != nil {
		t.Fatalf("the host answered %q", output)
	}
	if string(response.Answer) != `{"text":"rewritten"}` {
		t.Fatalf("the answer was %s", response.Answer)
	}
	request := fixture.languageModel.requests[0]
	if request.Messages[0].Role != "system" || request.Messages[0].Content != "You rewrite one unit." {
		t.Fatalf("the system message was %+v", request.Messages[0])
	}
	parts := request.Messages[1].Parts
	if len(parts) != 2 || parts[0].Text != "Unit: 최고의 서비스" || parts[1].MimeType != "image/png" || parts[1].DataBase64 != "UE5H" {
		t.Fatalf("the user message was %+v", parts)
	}
	if !request.StructuredOutputSchema.IsStrictlyEnforced || !strings.Contains(request.StructuredOutputSchema.Document, `"additionalProperties":false`) {
		t.Fatalf("the schema reached the model as %+v", request.StructuredOutputSchema)
	}
}

func TestACommandCallsARecordToolAsTheRequesterAndTheTaskRemembersIt(t *testing.T) {
	fixture := newScriptHostFixture(t)
	fixture.standIn.answered = aCompanyProfileAnswer()

	output := fixture.askTheHost(t, "tools/company_info_get", `{"language":"ko"}`)

	var response scriptToolResponse
	if errorValue := json.Unmarshal([]byte(output), &response); errorValue != nil {
		t.Fatalf("the host answered %q", output)
	}
	if fixture.standIn.calledTool != "company_info_get" || fixture.standIn.calledFor != "sample@example.com" {
		t.Fatalf("the record was asked %q for %q", fixture.standIn.calledTool, fixture.standIn.calledFor)
	}
	if response.IsError || len(response.Files) != 2 || filepath.Base(response.Files[0].Path) != "company-profile.json" {
		t.Fatalf("the host answered %+v", response)
	}
	if records := fixture.taskContextTheShellReads(t).Records; len(records) != 1 || records[0].Tool != "company_info_get" {
		t.Fatalf("the task context recorded %+v", records)
	}
}

func TestAToolTheRequesterCannotSeeIsRefused(t *testing.T) {
	fixture := newScriptHostFixture(t)

	output := fixture.askTheHost(t, "tools/payroll_export", `{}`)

	if !strings.Contains(output, `"error"`) || fixture.standIn.calledTool != "" {
		t.Fatalf("an undiscovered tool was called: %q", output)
	}
}

func TestEachAnswerIsRecordedInTheTaskLedger(t *testing.T) {
	fixture := newScriptHostFixture(t)

	fixture.askTheHost(t, "decide", choiceQuestionBody)

	for _, taskEvent := range fixture.builder.taskRunService.ListTaskEvent(fixture.taskRun.TaskRunID) {
		if taskEvent.Name == TaskEventScriptHostAnswered && strings.Contains(taskEvent.Body, `"model":"jev"`) && strings.Contains(taskEvent.Body, `"costUSD":0.002`) {
			return
		}
	}
	t.Fatal("the answer left no event in the task ledger")
}

func TestTheGrantEndsWithTheCommand(t *testing.T) {
	fixture := newScriptHostFixture(t)
	tokenPath := filepath.Join(fixture.homePath(), "token")
	fixture.shellOutput(t, `printf '%s' "$SKILL_HOST_TOKEN" > `+shellSingleQuoted(tokenPath))
	token, errorValue := os.ReadFile(tokenPath)
	if errorValue != nil || len(token) == 0 {
		t.Fatalf("the command was given no token: %v", errorValue)
	}

	request, _ := http.NewRequest(http.MethodPost, fixture.server.URL+"/decide", strings.NewReader(choiceQuestionBody))
	request.Header.Set("Authorization", "Bearer "+string(token))
	response, errorValue := http.DefaultClient.Do(request)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	response.Body.Close()

	if response.StatusCode != http.StatusUnauthorized || len(fixture.decisionModel.requests) != 0 {
		t.Fatalf("a token outlived its command: status %d", response.StatusCode)
	}
}

func TestTheModelCannotPointACommandAtAnotherHost(t *testing.T) {
	for _, name := range []string{ScriptHostURLEnvironmentName, ScriptHostTokenEnvironmentName} {
		if !security.IsWorkspaceManagedEnvironmentName(name) {
			t.Fatalf("%s is not managed by the workspace, so a command's own environment could replace it", name)
		}
	}
}

func TestAHostWithoutAScriptHostGivesCommandsNoAddress(t *testing.T) {
	fixture := newTaskFixture(t)

	if output := fixture.shellOutput(t, `printf '%s' "$SKILL_HOST_URL"`); output != "" {
		t.Fatalf("a host with no script host gave the command %q", output)
	}
}
