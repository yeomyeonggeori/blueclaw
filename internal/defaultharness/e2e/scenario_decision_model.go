//go:build !nobundledharness

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement/gatewaytest"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

const scenarioIgnoringAddressingResponse = `{"target":"anyone","shouldRespond":false,"dutyMatch":false,"dutyName":"","dutyConfidence":0}`

const scenarioDecisionModelName = "scenario-decision-model"

type scenarioTurnScript struct {
	turnIndex     int
	turnDocuments []string
}

func (turnScript *scenarioTurnScript) beginTurn(turnIndex int, turnDocuments []string) {
	turnScript.turnIndex = turnIndex
	turnScript.turnDocuments = append([]string{}, turnDocuments...)
}

func (turnScript *scenarioTurnScript) next() (string, error) {
	if len(turnScript.turnDocuments) == 0 {
		return "", fmt.Errorf("turn %d asked for a decision its script does not hold", turnScript.turnIndex)
	}
	turnDocument := turnScript.turnDocuments[0]
	turnScript.turnDocuments = turnScript.turnDocuments[1:]
	return turnDocument, nil
}

func (turnScript *scenarioTurnScript) pendingCount() int {
	return len(turnScript.turnDocuments)
}

type scenarioDecisionModel struct {
	turnScript         *scenarioTurnScript
	languageModelTurn  *intaketest.LanguageModelDecisionModel
	addressing         inboundengagement.AddressingDecision
	isAddressingScript bool

	mutex          sync.Mutex
	decidedOutcome intaketest.Outcome
	hasDecidedTurn bool
}

func newScenarioDecisionModel(turnScript *scenarioTurnScript, languageModel model.LanguageModelProvider, addressingResponse string) *scenarioDecisionModel {
	addressing := scenarioAddressingDecision(addressingResponse)
	return &scenarioDecisionModel{
		turnScript: turnScript,
		languageModelTurn: &intaketest.LanguageModelDecisionModel{
			LanguageModel: languageModel,
			ModelName:     scenarioDecisionModelName,
		},
		addressing:         addressing,
		isAddressingScript: strings.TrimSpace(addressingResponse) != "",
	}
}

func (decisionModel *scenarioDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if decisionModel.turnScript == nil {
		return decisionModel.languageModelTurn.Decide(ctx, request)
	}
	if inboundengagement.AsksOnlyGatewayQuestions(request.Questions) {
		return model.DecisionResponse{Answers: gatewaytest.Answers(request.Questions, decisionModel.gatewayOutcome(request)), ModelName: scenarioDecisionModelName}, nil
	}
	if outcome, isDecided := decisionModel.decidedTurn(); isDecided && asksOnlyAboutTools(request.Questions) {
		return decisionModel.answersFrom(request, outcome), nil
	}
	turnDocument, errorValue := decisionModel.turnScript.next()
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	var turnDecision turnclassification.TurnDecision
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(turnDocument)), &turnDecision); errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	outcome := intaketest.Outcome{TurnDecision: turnDecision}
	decisionModel.rememberDecidedTurn(outcome)
	return decisionModel.answersFrom(request, outcome), nil
}

func (decisionModel *scenarioDecisionModel) answersFrom(request model.DecisionRequest, outcome intaketest.Outcome) model.DecisionResponse {
	return model.DecisionResponse{
		Answers:   intaketest.Answers(request.Questions, func(string) intaketest.Outcome { return outcome }),
		ModelName: scenarioDecisionModelName,
	}
}

func (decisionModel *scenarioDecisionModel) decidedTurnReadingTheScript() (intaketest.Outcome, bool) {
	if decisionModel.turnScript != nil && decisionModel.turnScript.pendingCount() > 0 {
		decisionModel.decideNextScriptedTurn()
	}
	return decisionModel.decidedTurn()
}

func (decisionModel *scenarioDecisionModel) decideNextScriptedTurn() {
	turnDocument, errorValue := decisionModel.turnScript.next()
	if errorValue != nil {
		return
	}
	var turnDecision turnclassification.TurnDecision
	if json.Unmarshal([]byte(strings.TrimSpace(turnDocument)), &turnDecision) != nil {
		return
	}
	decisionModel.rememberDecidedTurn(intaketest.Outcome{TurnDecision: turnDecision})
}

func (decisionModel *scenarioDecisionModel) decidedTurn() (intaketest.Outcome, bool) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	return decisionModel.decidedOutcome, decisionModel.hasDecidedTurn
}

func (decisionModel *scenarioDecisionModel) rememberDecidedTurn(outcome intaketest.Outcome) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	decisionModel.decidedOutcome = outcome
	decisionModel.hasDecidedTurn = true
}

func asksOnlyAboutTools(questions map[string]model.DecisionQuestion) bool {
	for questionName := range questions {
		if !strings.Contains(questionName, "."+agentcontract.IntakeQuestionPrefixTool) {
			return false
		}
	}
	return len(questions) > 0
}

func (decisionModel *scenarioDecisionModel) gatewayOutcome(request model.DecisionRequest) gatewaytest.Outcome {
	if !decisionModel.isAddressingScript && isDirectPlacement(request) {
		return gatewaytest.Outcome{Addressing: scenarioDirectRequest, BusyRoute: inboundengagement.BusyRouteNewTask}
	}
	return gatewaytest.Outcome{Addressing: decisionModel.addressing, BusyRoute: inboundengagement.BusyRouteNewTask}
}

var scenarioDirectRequest = inboundengagement.AddressingDecision{Target: inboundengagement.AddressingTargetBot, ShouldRespond: true, HasWork: true}

func isDirectPlacement(request model.DecisionRequest) bool {
	document, errorValue := json.Marshal(request.State)
	if errorValue != nil {
		return false
	}
	var state struct {
		Placement string `json:"placement"`
	}
	return json.Unmarshal(document, &state) == nil && state.Placement == "direct"
}

func scenarioAddressingDecision(addressingResponse string) inboundengagement.AddressingDecision {
	document := strings.TrimSpace(addressingResponse)
	if document == "" {
		document = scenarioIgnoringAddressingResponse
	}
	var decision inboundengagement.AddressingDecision
	if errorValue := json.Unmarshal([]byte(document), &decision); errorValue != nil {
		return inboundengagement.AddressingDecision{}
	}
	return decision
}

type scenarioReplyReader struct {
	decisionModel *scenarioDecisionModel
}

func (reader scenarioReplyReader) Read(_ context.Context, question approvalreply.Question, _ string, _ agentcontract.LLMCallObserver) (string, bool, error) {
	if reader.decisionModel == nil {
		return "", false, nil
	}
	outcome, isDecided := reader.decisionModel.decidedTurnReadingTheScript()
	if !isDecided {
		return "", false, nil
	}
	return scriptedOption(question.Options, outcome.TurnDecision)
}

func scriptedOption(options []approvalreply.Option, turnDecision turnclassification.TurnDecision) (string, bool, error) {
	for _, option := range options {
		if isScriptedOption(option, turnDecision) {
			return option.ID, true, nil
		}
	}
	return "", false, nil
}

func isScriptedOption(option approvalreply.Option, turnDecision turnclassification.TurnDecision) bool {
	if len(turnDecision.Choices) > 0 {
		return option.ID == turnDecision.Choices[0] || strings.HasSuffix(option.ID, ":"+turnDecision.Choices[0])
	}
	isRejecting := option.Meaning == approvalreply.RejectMeaning
	if turnDecision.Approval == nil {
		return false
	}
	return isRejecting == (*turnDecision.Approval == agentcontract.ApprovalSignalReject)
}

type splitDecisionModel struct {
	planning model.DecisionModel
	loop     model.DecisionModel
}

func scenarioHarnessDecisionModel(scenario VirtualSessionScenario, planning model.DecisionModel, loop model.DecisionModel) model.DecisionModel {
	if scenario.DecisionModel != nil {
		return planning
	}
	return splitDecisionModel{planning: planning, loop: loop}
}

func (decisionModel splitDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if asksPlanningQuestions(request.Questions) {
		return decisionModel.planning.Decide(ctx, request)
	}
	if decisionModel.loop == nil {
		return model.DecisionResponse{}, errors.New("decision model is not configured")
	}
	return decisionModel.loop.Decide(ctx, request)
}

func asksPlanningQuestions(questions map[string]model.DecisionQuestion) bool {
	for questionName := range questions {
		if strings.Contains(questionName, ".") {
			return true
		}
	}
	return false
}
