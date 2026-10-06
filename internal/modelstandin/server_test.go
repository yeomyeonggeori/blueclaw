package modelstandin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

type standIn struct {
	server     *Server
	httpServer *httptest.Server
}

func startStandIn(t *testing.T) standIn {
	t.Helper()
	server := NewServer(io.Discard)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return standIn{server: server, httpServer: httpServer}
}

func (running standIn) script(t *testing.T, path string, document any) *http.Response {
	t.Helper()
	body, errorValue := json.Marshal(document)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	response, errorValue := http.Post(running.httpServer.URL+path, "application/json", bytes.NewReader(body))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func (running standIn) mustScript(t *testing.T, path string, document any) {
	t.Helper()
	if response := running.script(t, path, document); response.StatusCode != http.StatusNoContent {
		t.Fatalf("scripting %s answered %d", path, response.StatusCode)
	}
}

func (running standIn) languageModel() *openaicompatible.Provider {
	return openaicompatible.NewProvider(running.httpServer.URL+"/v1", "stand-in-key", "stand-in/model")
}

func (running standIn) decisionModel() model.DecisionModel {
	return decisions.Endpoint{
		URL:       running.httpServer.URL + decisionsPath(),
		ModelName: "stand-in/decision",
		APIKey:    "stand-in-key",
	}.DecisionModel()
}

func (running standIn) mustBeSettled(t *testing.T) {
	t.Helper()
	leftovers := running.server.Leftovers()
	if len(leftovers.Refusals) > 0 || len(leftovers.Unconsumed) > 0 {
		t.Fatalf("expected nothing left over, got %+v", leftovers)
	}
}

func (running standIn) mustHaveRefused(t *testing.T, fragment string) {
	t.Helper()
	for _, refusal := range running.server.Leftovers().Refusals {
		if strings.Contains(refusal, fragment) {
			return
		}
	}
	t.Fatalf("expected a refusal mentioning %q, got %+v", fragment, running.server.Leftovers())
}

func replyTool() model.ChatCompletionTool {
	return model.ChatCompletionTool{Type: "function", Function: model.ChatCompletionFunction{Name: "reply", Parameters: json.RawMessage(`{"type":"object"}`)}}
}

type plannedAnswers struct {
	route     string
	toolNames []string
}

func planTheAddressedMessage(running standIn) (plannedAnswers, error) {
	state := map[string]any{"messages": []map[string]string{{"text": "업무로 남겨줘"}}}
	decisionModel := running.decisionModel()
	routeResponse, errorValue := decisionModel.Decide(context.Background(), model.DecisionRequest{State: state, Questions: map[string]model.DecisionQuestion{
		"m1." + agentcontract.IntakeQuestionRoute: model.ChoiceQuestion{Instructions: "route"}.Question(),
	}})
	if errorValue != nil {
		return plannedAnswers{}, errorValue
	}
	toolResponse, errorValue := decisionModel.Decide(context.Background(), model.DecisionRequest{State: state, Questions: map[string]model.DecisionQuestion{
		"m1.tool.task_add":     model.NoulQuestion{Instructions: "task_add"}.Question(),
		"m1.tool.message_send": model.NoulQuestion{Instructions: "message_send"}.Question(),
	}})
	if errorValue != nil {
		return plannedAnswers{}, errorValue
	}
	planned := plannedAnswers{route: routeResponse.Answers["m1."+agentcontract.IntakeQuestionRoute].Choice}
	for _, toolName := range []string{"task_add", "message_send"} {
		if toolResponse.Answers["m1.tool."+toolName].Noul >= 0.5 {
			planned.toolNames = append(planned.toolNames, toolName)
		}
	}
	return planned, nil
}

func TestAStructuredCallIsAnsweredWithTheDocumentScriptedForItsSchema(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/structured", map[string]any{"schemaName": "bluecollar_turn_router", "document": map[string]any{"expectedResults": []any{}}})

	response, errorValue := running.languageModel().GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		Messages:               []model.Message{{Role: "user", Content: "words"}},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_turn_router", Document: `{"type":"object"}`},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.Content != `{"expectedResults":[]}` {
		t.Fatalf("expected the scripted document, got %s", response.Content)
	}
	running.mustBeSettled(t)
}

func TestAnAgentActionIsAnsweredAsANativeCallOfTheScriptedTool(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/action", map[string]any{"toolName": "reply", "arguments": map[string]any{"message": "done", "final": true}})

	response, errorValue := running.languageModel().GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{
		Messages:   []model.ChatCompletionMessage{{Role: "user", Content: "finish"}},
		Tools:      []model.ChatCompletionTool{replyTool()},
		ToolChoice: json.RawMessage(`"required"`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCall := response.Message.ToolCalls[0]
	if toolCall.Function.Name != "reply" || toolCall.Function.Arguments != `{"final":true,"message":"done"}` {
		t.Fatalf("expected the scripted reply call, got %+v", toolCall)
	}
	running.mustBeSettled(t)
}

func TestAnAgentActionNamingAToolTheRequestDidNotOfferIsRefused(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/action", map[string]any{"toolName": "message_send", "arguments": map[string]any{"message": "hello"}})

	_, errorValue := running.languageModel().GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{
		Messages: []model.ChatCompletionMessage{{Role: "user", Content: "send it"}},
		Tools:    []model.ChatCompletionTool{replyTool()},
	})
	if errorValue == nil {
		t.Fatal("expected a scripted call to a tool nobody offered to fail")
	}
	running.mustHaveRefused(t, `"message_send" is not exposed; available tools: reply`)
}

func TestAStructuredCallNoScriptAnswersIsRefused(t *testing.T) {
	running := startStandIn(t)

	_, errorValue := running.languageModel().GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_turn_router", Document: `{"type":"object"}`},
	})
	if errorValue == nil {
		t.Fatal("expected an unscripted schema to fail")
	}
	running.mustHaveRefused(t, "has no bluecollar_turn_router response")
}

func TestACompletionOfferingNoToolsIsRefused(t *testing.T) {
	running := startStandIn(t)

	_, errorValue := running.languageModel().GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{
		Messages: []model.ChatCompletionMessage{{Role: "user", Content: "write the reply"}},
	})
	if errorValue == nil {
		t.Fatal("expected a completion offering no tools to fail")
	}
	running.mustHaveRefused(t, "offering no tools")
}

func TestIntakeReadsTheScriptedTurnAndItsToolsThroughTheDecisionsEndpoint(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/turn", map[string]any{
		"message":      "업무로 남겨줘",
		"addressing":   map[string]any{"target": "bot", "shouldRespond": true},
		"turnDecision": map[string]any{"route": "start_task", "classification": "bounded_task", "initialToolNames": []string{"task_add"}},
	})

	planned, errorValue := planTheAddressedMessage(running)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if planned.route != string(agentcontract.TurnRouteStartTask) {
		t.Fatalf("expected a start_task turn, got %+v", planned)
	}
	if !slices.Equal(planned.toolNames, []string{"task_add"}) {
		t.Fatalf("expected the scripted tool and no other, got %v", planned.toolNames)
	}
	if asked := running.server.asked; len(asked) != 2 || !slices.Contains(asked[1].QuestionNames, "m1.tool.task_add") {
		t.Fatalf("expected the turn and then its tool selection to be asked, got %+v", asked)
	}
	running.mustBeSettled(t)
}

func TestTheGatewayReadsTheScriptedAddressingAndLeavesTheTurnToBePlanned(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/turn", map[string]any{
		"message":      "업무로 남겨줘",
		"addressing":   map[string]any{"target": "bot", "shouldRespond": true},
		"turnDecision": map[string]any{"route": "start_task", "classification": "bounded_task"},
	})
	decider := inboundengagement.NewDecisionModelDecider(running.decisionModel(), nil)

	judgments, errorValue := decider.Decide(context.Background(), aChannelMessageFacts("업무로 남겨줘"), nil)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(judgments) != 1 || !judgments[0].Addressing.ShouldRespond {
		t.Fatalf("expected the scripted addressing, got %+v", judgments)
	}
	if unconsumed := running.server.Leftovers().Unconsumed[unconsumedTurnsName]; unconsumed != 1 {
		t.Fatalf("expected the turn to wait for its plan, got %d unconsumed", unconsumed)
	}
}

func TestAnOverheardMessageSettlesItsScriptAtTheGateway(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/turn", map[string]any{"message": "점심 뭐 먹지", "addressing": map[string]any{"target": "anyone", "shouldRespond": false}})
	decider := inboundengagement.NewDecisionModelDecider(running.decisionModel(), nil)

	judgments, errorValue := decider.Decide(context.Background(), aChannelMessageFacts("점심 뭐 먹지"), nil)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(judgments) != 1 || judgments[0].Addressing.ShouldRespond {
		t.Fatalf("expected the scripted overheard judgment, got %+v", judgments)
	}
	running.mustBeSettled(t)
}

func TestAnApprovalReplyIsReadAsTheOptionTheScriptedApprovalPicks(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/turn", map[string]any{"message": "응 보내줘", "turnDecision": map[string]any{"route": "continue_task", "approval": "approve"}})
	question := approvalreply.Question{Text: "send it?", Options: []approvalreply.Option{
		{ID: "approve", Meaning: approvalreply.AllowMeaning("send it")},
		{ID: "reject", Meaning: approvalreply.RejectMeaning},
	}}

	optionID, isAnswer, errorValue := approvalreply.NewDecisionModelReader(running.decisionModel()).Read(context.Background(), question, "응 보내줘", nil)

	if errorValue != nil || !isAnswer || optionID != "approve" {
		t.Fatalf("expected the reply to be read as approve, got %q %v %v", optionID, isAnswer, errorValue)
	}
	running.mustBeSettled(t)
}

func aChannelMessageFacts(words string) inboundengagement.Facts {
	return inboundengagement.Facts{
		ConversationType: "channel",
		AgentIdentity:    agentcontract.AgentIdentity{Name: "인턴"},
		Messages:         []inboundengagement.Message{{MessageID: "message-1", Prompt: words, SenderName: "이샘플"}},
	}
}

func TestAnApprovalReplyNoScriptDecidedIsRefused(t *testing.T) {
	running := startStandIn(t)
	question := approvalreply.Question{Text: "send it?", Options: []approvalreply.Option{{ID: "approve", Meaning: approvalreply.AllowMeaning("send it")}}}

	_, _, errorValue := approvalreply.NewDecisionModelReader(running.decisionModel()).Read(context.Background(), question, "응 보내줘", nil)

	if errorValue == nil {
		t.Fatal("expected a reply nobody scripted to fail")
	}
	running.mustHaveRefused(t, `a reply no script decided was read as an answer to an approval question: "응 보내줘"`)
}

func TestATurnNoScriptDecidedIsRefused(t *testing.T) {
	running := startStandIn(t)

	_, errorValue := planTheAddressedMessage(running)
	if errorValue == nil {
		t.Fatal("expected a turn nobody scripted to fail")
	}
	running.mustHaveRefused(t, "a turn no script decided asked")
}

func TestADecisionQuestionIntaketestCannotAnswerIsRefusedWithoutSpendingATurn(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/turn", map[string]any{"message": "업무로 남겨줘", "turnDecision": map[string]any{"route": "start_task"}})

	_, errorValue := running.decisionModel().Decide(context.Background(), model.DecisionRequest{
		Questions: map[string]model.DecisionQuestion{"expected0": {Type: model.DecisionQuestionTypeNoul}},
	})
	if errorValue == nil {
		t.Fatal("expected a question no script answers to fail")
	}
	running.mustHaveRefused(t, "no script answers the decision questions expected0")
	if unconsumed := running.server.Leftovers().Unconsumed[unconsumedTurnsName]; unconsumed != 1 {
		t.Fatalf("expected the scripted turn to still be waiting, got %d", unconsumed)
	}
}

func TestScriptsNobodyAskedForAreLeftOver(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/turn", map[string]any{"message": "업무로 남겨줘", "turnDecision": map[string]any{"route": "start_task"}})
	running.mustScript(t, "/script/structured", map[string]any{"schemaName": "bluecollar_execution_plan", "document": map[string]any{}})
	running.mustScript(t, "/script/action", map[string]any{"toolName": "reply", "arguments": map[string]any{"final": true}})

	unconsumed := running.server.Leftovers().Unconsumed
	expected := map[string]int{unconsumedTurnsName: 1, "bluecollar_execution_plan": 1, agentcontract.AgentActionSchemaName: 1}
	for name, count := range expected {
		if unconsumed[name] != count {
			t.Fatalf("expected %d %s left over, got %v", count, name, unconsumed)
		}
	}

	running.mustScript(t, "/script/reset", map[string]any{})
	running.mustBeSettled(t)
}

func TestATurnScriptInAnotherVocabularyIsRejected(t *testing.T) {
	running := startStandIn(t)

	response := running.script(t, "/script/turn", map[string]any{"message": "응 보내줘", "turnDecision": map[string]any{"route": "start_task", "approvalSignal": "approve"}})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected a field TurnDecision does not have to be rejected, got %d", response.StatusCode)
	}
}

func TestATurnScriptNamingNoMessageIsRejected(t *testing.T) {
	running := startStandIn(t)

	response := running.script(t, "/script/turn", map[string]any{"turnDecision": map[string]any{"route": "start_task"}})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected a turn that names no message to be rejected, got %d", response.StatusCode)
	}
}

func TestATurnScriptedForAnotherMessageIsRefusedWithoutBeingSpent(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/turn", map[string]any{"message": "응 보내줘", "turnDecision": map[string]any{"route": "continue_task"}})

	_, errorValue := planTheAddressedMessage(running)
	if errorValue == nil {
		t.Fatal("expected a turn scripted for another message to fail")
	}
	running.mustHaveRefused(t, `the next scripted turn decides "응 보내줘", but intake asked about "업무로 남겨줘"`)
	if unconsumed := running.server.Leftovers().Unconsumed[unconsumedTurnsName]; unconsumed != 1 {
		t.Fatalf("expected the scripted turn to still be waiting, got %d", unconsumed)
	}
}

func TestEveryAskIsRecordedWithTheModelAndKeyItCarried(t *testing.T) {
	running := startStandIn(t)
	running.mustScript(t, "/script/structured", map[string]any{"schemaName": "bluecollar_turn_router", "document": map[string]any{}})
	if _, errorValue := running.languageModel().GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_turn_router", Document: `{"type":"object"}`},
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	response, errorValue := http.Get(running.httpServer.URL + "/asked")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer response.Body.Close()
	var asked []Ask
	if errorValue := json.NewDecoder(response.Body).Decode(&asked); errorValue != nil {
		t.Fatal(errorValue)
	}
	expected := Ask{Kind: AskKindCompletion, Model: "stand-in/model", Authorization: "Bearer stand-in-key", SchemaName: "bluecollar_turn_router", OfferedToolNames: []string{"bluecollar_turn_router"}, AnsweredWith: "bluecollar_turn_router"}
	if len(asked) != 1 || !askEquals(asked[0], expected) {
		t.Fatalf("expected %+v, got %+v", expected, asked)
	}
}

func askEquals(left Ask, right Ask) bool {
	return left.Kind == right.Kind &&
		left.Model == right.Model &&
		left.Authorization == right.Authorization &&
		left.SchemaName == right.SchemaName &&
		slices.Equal(left.OfferedToolNames, right.OfferedToolNames) &&
		left.AnsweredWith == right.AnsweredWith
}

func TestSettlingAMemoryNoteNeedsNoScriptBecauseTheStandInRemembersNothing(t *testing.T) {
	running := startStandIn(t)
	store, errorValue := bluememo.Open(context.Background(), filepath.Join(t.TempDir(), "memory.sqlite"), bluememo.Configuration{
		Embedder: bluememotest.HashEmbedder{},
		Model:    memory.LanguageModel{Provider: running.languageModel()},
		Judge:    &bluememotest.ScriptedJudge{},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { store.Close() })
	if errorValue := store.Memorize(context.Background(), bluememo.Note{Body: "Asked: say hello"}); errorValue != nil {
		t.Fatal(errorValue)
	}

	report, errorValue := store.Settle(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if report.Groups != 1 || report.Proposed != 0 {
		t.Fatalf("expected one group settled with nothing proposed, got %+v", report)
	}
	running.mustBeSettled(t)
}
