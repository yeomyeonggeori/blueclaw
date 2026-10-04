package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

type dataRoomStandIn struct {
	discovered  []capability.ToolDescriptor
	answers     map[string]mcp.ToolResult
	calledTools []string
}

func (standIn *dataRoomStandIn) DiscoverTools(context.Context, string) ([]capability.ToolDescriptor, error) {
	return standIn.discovered, nil
}

func (standIn *dataRoomStandIn) CallTool(_ context.Context, _ string, toolName string, _ json.RawMessage) (mcp.ToolResult, error) {
	standIn.calledTools = append(standIn.calledTools, toolName)
	return standIn.answers[toolName], nil
}

type designDecisionStandIn struct {
	asked []model.DecisionRequest
}

func (standIn *designDecisionStandIn) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	standIn.asked = append(standIn.asked, request)
	answers := map[string]model.DecisionAnswer{}
	for name := range request.Questions {
		answers[name] = model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: "first", Probabilities: map[string]float64{"first": 0.8, "second": 0.2}}
	}
	return model.DecisionResponse{Answers: answers, ModelName: "design-stand-in", Usage: model.Usage{CostUSD: 0.001}}, nil
}

func dataRoomDescriptor(name string) capability.ToolDescriptor {
	descriptor := aDescriptor(name, capability.AnsweredByRecord)
	descriptor.SideEffectClass = toolcontract.ToolSideEffectRead
	descriptor.InputSchema = json.RawMessage(`{"type":"object","additionalProperties":true}`)
	descriptor.InputIntentSchema = descriptor.InputSchema
	descriptor.ResultContract = &capability.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)}
	return descriptor
}

func samplePNG(t *testing.T, width int, height int) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if errorValue := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, height))); errorValue != nil {
		t.Fatal(errorValue)
	}
	return encoded.Bytes()
}

type deckPreparationFixture struct {
	officeContextFixture
	dataRoom  *dataRoomStandIn
	decisions *designDecisionStandIn
}

func newDeckPreparationFixture(t *testing.T) deckPreparationFixture {
	t.Helper()
	descriptors := []capability.ToolDescriptor{dataRoomDescriptor(companyDocumentListToolName), dataRoomDescriptor(companyDocumentDownloadToolName)}
	fixture := newOfficeContextFixture(t, descriptors...)
	photo := samplePNG(t, 30, 20)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(photo) }))
	t.Cleanup(server.Close)
	dataRoom := &dataRoomStandIn{discovered: descriptors, answers: map[string]mcp.ToolResult{
		companyDocumentListToolName: {StructuredContent: json.RawMessage(`{"result":{"documents":[
			{"documentID":"d-1","title":"Greenhouse rows","summary":"Strawberry rows in the greenhouse","date":"2026-05-03","filePath":"SM/greenhouse.d-1.png","domain":"marketing"},
			{"documentID":"d-2","title":"Lease","summary":null,"date":"2026-06-01","filePath":"CO/lease.d-2.pdf","domain":"contracts"}]}}`)},
		companyDocumentDownloadToolName: {StructuredContent: json.RawMessage(`{"result":{"downloadURL":"` + server.URL + `/greenhouse.png","storagePath":"SM/greenhouse.d-1.png"}}`)},
	}}
	fixture.request.RecordCatalog = dataRoom
	fixture.request.Prompt = "딸기 농가 지원사업 발표자료를 만들어 주세요"
	decisions := &designDecisionStandIn{}
	fixture.builder.UseDeckDesignModel(decisions)
	return deckPreparationFixture{officeContextFixture: fixture, dataRoom: dataRoom, decisions: decisions}
}

func (fixture deckPreparationFixture) requestPreparation(t *testing.T) {
	t.Helper()
	designPath := filepath.Join(t.TempDir(), "design.json")
	definition := `{"instructions":"Choose from the request.","questions":{"palette":{"instructions":"Which color?","options":{"first":"A blue.","second":"A green."}},"cover":{"instructions":"What carries the cover?","options":{"first":"A photo.","second":"The title."}}}}`
	if errorValue := os.WriteFile(designPath, []byte(definition), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	taskDirectoryPath := security.TaskTemporaryDirectoryPath(fixture.homePath(), fixture.taskRun.TaskRunID)
	if errorValue := os.MkdirAll(taskDirectoryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	request, _ := json.Marshal(officeDeckPreparationRequest{Design: designPath})
	if errorValue := os.WriteFile(filepath.Join(taskDirectoryPath, officeContract.DeckPreparation.RequestFile), request, 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestTheRuntimeContextSaysTheHostPreparesDecksOnlyWithADesignModel(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	if prepares := string(fixture.contextTheShellReads(t)["preparesDecks"]); prepares != "false" {
		t.Fatalf("a host with no design model told the office it prepares decks: %s", prepares)
	}
	fixture.builder.UseDeckDesignModel(&designDecisionStandIn{})
	if prepares := string(fixture.contextTheShellReads(t)["preparesDecks"]); prepares != "true" {
		t.Fatalf("a host with a design model did not tell the office it prepares decks: %s", prepares)
	}
}

func TestNoDeckIsPreparedUntilTheOfficeAsks(t *testing.T) {
	fixture := newDeckPreparationFixture(t)

	document := fixture.contextTheShellReads(t)

	if len(fixture.decisions.asked) != 0 || len(fixture.dataRoom.calledTools) != 0 {
		t.Fatalf("an unrequested deck was prepared: %d decisions, tools %v", len(fixture.decisions.asked), fixture.dataRoom.calledTools)
	}
	if string(document["deckDesign"]) != "null" || string(document["images"]) != "[]" {
		t.Fatalf("an unprepared deck carried a design %s and images %s", document["deckDesign"], document["images"])
	}
}

func TestARequestedDeckIsDesignedInOneDecisionWithTheDataRoomPhotosTheRequesterCanRead(t *testing.T) {
	fixture := newDeckPreparationFixture(t)
	fixture.requestPreparation(t)

	document := fixture.contextTheShellReads(t)
	fixture.contextTheShellReads(t)

	if len(fixture.decisions.asked) != 1 || len(fixture.decisions.asked[0].Questions) != 2 {
		t.Fatalf("the design was asked %d times, expected once with both questions", len(fixture.decisions.asked))
	}
	var design officeDeckDesign
	_ = json.Unmarshal(document["deckDesign"], &design)
	if design.Choices["palette"].Option != "first" || design.Choices["palette"].Probabilities["second"] != 0.2 || design.Model != "design-stand-in" {
		t.Fatalf("the recorded design was %s", document["deckDesign"])
	}
	var images []officeImage
	_ = json.Unmarshal(document["images"], &images)
	if len(images) != 1 || images[0].Width != 30 || images[0].Height != 20 || images[0].Title != "Greenhouse rows" || images[0].Folder != "marketing" || images[0].Source != officeImageSourceDataRoom {
		t.Fatalf("the gathered images were %s", document["images"])
	}
	if _, errorValue := os.Stat(images[0].Path); errorValue != nil {
		t.Fatalf("the kept photo is not where the context says: %v", errorValue)
	}
	state, _ := json.Marshal(fixture.decisions.asked[0].State)
	if !bytes.Contains(state, []byte("Greenhouse rows")) || !bytes.Contains(state, []byte(fixture.request.Prompt)) {
		t.Fatalf("the design was decided without the request and the photos: %s", state)
	}
}

func TestTheDesignIsDecidedWithTheImagesTheRequestAttached(t *testing.T) {
	fixture := newDeckPreparationFixture(t)
	fixture.request.VisibleContext = agentcontract.VisibleContext{CurrentMaterials: []agentcontract.VisibleContextMaterial{
		{MaterialID: "m-1", Filename: "factory-floor.jpg", Path: filepath.Join(fixture.homePath(), "inbox", "factory-floor.jpg"), IsAvailable: true},
		{MaterialID: "m-2", Filename: "budget.csv", Path: filepath.Join(fixture.homePath(), "inbox", "budget.csv"), IsAvailable: true},
	}}
	fixture.requestPreparation(t)

	fixture.contextTheShellReads(t)

	state, _ := json.Marshal(fixture.decisions.asked[0].State)
	if !bytes.Contains(state, []byte("factory-floor.jpg")) || bytes.Contains(state, []byte("budget.csv")) {
		t.Fatalf("the design was decided without exactly the attached images: %s", state)
	}
}

type failingDecisionStandIn struct{}

func (failingDecisionStandIn) Decide(context.Context, model.DecisionRequest) (model.DecisionResponse, error) {
	return model.DecisionResponse{}, errors.New("the decision model answered 503")
}

func TestAFailedDesignDecisionRecordsTheFailureAndNoChoices(t *testing.T) {
	fixture := newDeckPreparationFixture(t)
	fixture.builder.UseDeckDesignModel(failingDecisionStandIn{})
	fixture.requestPreparation(t)

	document := fixture.contextTheShellReads(t)

	var design struct {
		Choices map[string]json.RawMessage `json:"choices"`
		Failure string                     `json:"failure"`
	}
	if errorValue := json.Unmarshal(document["deckDesign"], &design); errorValue != nil || design.Choices == nil || len(design.Choices) != 0 || design.Failure == "" {
		t.Fatalf("a failed decision was recorded as %s", document["deckDesign"])
	}
}
