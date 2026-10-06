package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
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
	turnScript        *scenarioTurnScript
	languageModelTurn *intaketest.LanguageModelDecisionModel
	addressing        agentcontract.AddressingDecision

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
			Addressing:    addressing,
			ModelName:     scenarioDecisionModelName,
		},
		addressing: addressing,
	}
}

func (decisionModel *scenarioDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if decisionModel.turnScript == nil {
		return decisionModel.languageModelTurn.Decide(ctx, request)
	}
	if inboundengagement.AsksOnlyGatewayQuestions(request.Questions) {
		return decisionModel.answersFrom(request, decisionModel.gatewayOutcome()), nil
	}
	if outcome, isDecided := decisionModel.decidedTurn(); isDecided && asksOnlyAboutTools(request.Questions) {
		return decisionModel.answersFrom(request, outcome), nil
	}
	turnDocument, errorValue := decisionModel.turnScript.next()
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	var turnDecision agentcontract.TurnDecision
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(turnDocument)), &turnDecision); errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	outcome := intaketest.Outcome{
		Addressing:        decisionModel.addressing,
		TurnDecision:      turnDecision,
		PendingChoiceKeys: intaketest.PendingChoiceKeys(request.State),
	}
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
	var turnDecision agentcontract.TurnDecision
	if json.Unmarshal([]byte(strings.TrimSpace(turnDocument)), &turnDecision) != nil {
		return
	}
	decisionModel.rememberDecidedTurn(intaketest.Outcome{Addressing: decisionModel.addressing, TurnDecision: turnDecision})
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

func (decisionModel *scenarioDecisionModel) gatewayOutcome() intaketest.Outcome {
	return intaketest.Outcome{
		Addressing:   decisionModel.addressing,
		TurnDecision: agentcontract.TurnDecision{BusyRoute: agentcontract.BusyRouteNewTask},
	}
}

func scenarioAddressingDecision(addressingResponse string) agentcontract.AddressingDecision {
	document := strings.TrimSpace(addressingResponse)
	if document == "" {
		document = scenarioIgnoringAddressingResponse
	}
	var decision agentcontract.AddressingDecision
	if errorValue := json.Unmarshal([]byte(document), &decision); errorValue != nil {
		return agentcontract.AddressingDecision{}
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
	optionID, isScripted := approvalreply.ScriptedOptionID(question.Options, outcome.TurnDecision)
	return optionID, isScripted, nil
}
