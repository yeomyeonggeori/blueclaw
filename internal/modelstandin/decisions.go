package modelstandin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement/gatewaytest"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type decisionRequestDocument struct {
	Model     string                            `json:"model"`
	State     json.RawMessage                   `json:"state"`
	Questions map[string]model.DecisionQuestion `json:"questions"`
}

type decisionAnswerDocument struct {
	Choice        string             `json:"choice,omitempty"`
	Noul          float64            `json:"noul"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence"`
}

func (server *Server) serveDecision(writer http.ResponseWriter, request *http.Request) {
	var document decisionRequestDocument
	if errorValue := json.NewDecoder(request.Body).Decode(&document); errorValue != nil {
		http.Error(writer, "a decision request is JSON: "+errorValue.Error(), http.StatusBadRequest)
		return
	}
	ask := Ask{
		Kind:          AskKindDecision,
		Model:         document.Model,
		Authorization: request.Header.Get("Authorization"),
		QuestionNames: sortedQuestionNames(document.Questions),
		About:         truncated(decidedMessageText(document.State)),
	}
	answers, errorValue := server.decide(document)
	if errorValue != nil {
		server.refuse(writer, ask, errorValue.Error())
		return
	}
	server.record(ask)
	writeJSON(writer, map[string]any{
		"answers":  answerDocuments(answers),
		"usage":    map[string]any{"input_tokens": 1, "output_tokens": 1, "cost": 0},
		"model":    document.Model,
		"provider": "stand-in",
	})
}

func decidedMessageText(state json.RawMessage) string {
	var decisionState struct {
		Messages []struct {
			Text string `json:"text"`
		} `json:"messages"`
		Reply string `json:"reply"`
	}
	if json.Unmarshal(state, &decisionState) != nil {
		return ""
	}
	if len(decisionState.Messages) == 0 {
		return decisionState.Reply
	}
	texts := []string{}
	for _, message := range decisionState.Messages {
		texts = append(texts, message.Text)
	}
	return strings.Join(texts, " | ")
}

func (server *Server) decide(document decisionRequestDocument) (map[string]model.DecisionAnswer, error) {
	if len(document.Questions) == 0 {
		return nil, errors.New("a decision request asked no question")
	}
	if inboundengagement.AsksOnlyGatewayQuestions(document.Questions) {
		return server.decideGateway(document)
	}
	if isApprovalReading(document.Questions) {
		return server.readApprovalReply(document)
	}
	if _, unknownQuestionNames := intaketest.AnswersAndUnknownQuestions(document.Questions, noOutcome); len(unknownQuestionNames) > 0 {
		return nil, fmt.Errorf("no script answers the decision questions %s", strings.Join(unknownQuestionNames, ", "))
	}
	outcome, errorValue := server.outcomeFor(document)
	if errorValue != nil {
		return nil, errorValue
	}
	return intaketest.Answers(document.Questions, func(string) intaketest.Outcome { return outcome }), nil
}

func (server *Server) decideGateway(document decisionRequestDocument) (map[string]model.DecisionAnswer, error) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	if len(server.turns) == 0 {
		return nil, fmt.Errorf("a message no script decided asked the gateway about %q: %s", decidedMessageText(document.State), strings.Join(sortedQuestionNames(document.Questions), ", "))
	}
	nextTurn := server.turns[0]
	if askedAbout := decidedMessageText(document.State); askedAbout != nextTurn.Message {
		return nil, fmt.Errorf("the next scripted turn decides %q, but the gateway asked about %q", nextTurn.Message, askedAbout)
	}
	if !nextTurn.plansATurn() {
		server.turns = server.turns[1:]
	}
	return gatewaytest.Answers(document.Questions, nextTurn.gatewayOutcome()), nil
}

func noOutcome(string) intaketest.Outcome {
	return intaketest.Outcome{}
}

func (server *Server) outcomeFor(document decisionRequestDocument) (intaketest.Outcome, error) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	if selectsToolsOnly(document.Questions) {
		if server.decidedTurn == nil {
			return intaketest.Outcome{}, errors.New("tools were selected for a turn no script decided")
		}
		return *server.decidedTurn, nil
	}
	if len(server.turns) == 0 {
		return intaketest.Outcome{}, fmt.Errorf("a turn no script decided asked about %q: %s", decidedMessageText(document.State), strings.Join(sortedQuestionNames(document.Questions), ", "))
	}
	nextTurn, errorValue := server.popTurnDeciding(decidedMessageText(document.State))
	if errorValue != nil {
		return intaketest.Outcome{}, errorValue
	}
	outcome := nextTurn.Outcome
	server.decidedTurn = &outcome
	return outcome, nil
}

func (server *Server) popTurnDeciding(askedAbout string) (turnScript, error) {
	nextTurn := server.turns[0]
	if askedAbout != nextTurn.Message {
		return turnScript{}, fmt.Errorf("the next scripted turn decides %q, but intake asked about %q", nextTurn.Message, askedAbout)
	}
	server.turns = server.turns[1:]
	return nextTurn, nil
}

func isApprovalReading(questions map[string]model.DecisionQuestion) bool {
	_, isAsked := questions[approvalreply.AnswerQuestionName]
	return isAsked && len(questions) == 1
}

func (server *Server) readApprovalReply(document decisionRequestDocument) (map[string]model.DecisionAnswer, error) {
	reply := decidedMessageText(document.State)
	server.mutex.Lock()
	defer server.mutex.Unlock()
	if len(server.turns) == 0 {
		return nil, fmt.Errorf("a reply no script decided was read as an answer to an approval question: %q", reply)
	}
	scriptedTurn, errorValue := server.popTurnDeciding(reply)
	if errorValue != nil {
		return nil, errorValue
	}
	options := approvalOptions(document.Questions[approvalreply.AnswerQuestionName])
	chosenOptionID, isScripted := ScriptedOptionID(options, scriptedTurn.TurnDecision.Choices, scriptedTurn.TurnDecision.Approval)
	if !isScripted {
		chosenOptionID = approvalreply.OtherOptionID
	}
	return map[string]model.DecisionAnswer{approvalreply.AnswerQuestionName: {
		Type:          model.DecisionQuestionTypeChoice,
		Choice:        chosenOptionID,
		Probabilities: map[string]float64{chosenOptionID: 1},
		Confidence:    1,
	}}, nil
}

func approvalOptions(question model.DecisionQuestion) []approvalreply.Option {
	meaningByOptionID, _ := question.Criteria.(map[string]any)
	options := []approvalreply.Option{}
	for optionID, meaning := range meaningByOptionID {
		if optionID == approvalreply.OtherOptionID {
			continue
		}
		meaningText, _ := meaning.(string)
		options = append(options, approvalreply.Option{ID: optionID, Meaning: meaningText})
	}
	sort.Slice(options, func(first int, second int) bool { return options[first].ID < options[second].ID })
	return options
}

func selectsToolsOnly(questions map[string]model.DecisionQuestion) bool {
	for questionName := range questions {
		if !isToolSelectionQuestion(questionName) {
			return false
		}
	}
	return true
}

func isToolSelectionQuestion(questionName string) bool {
	_, shortName, _ := strings.Cut(questionName, ".")
	return strings.HasPrefix(shortName, agentcontract.IntakeQuestionPrefixTool) || shortName == agentcontract.IntakeQuestionSingleToolChoice
}

func sortedQuestionNames(questions map[string]model.DecisionQuestion) []string {
	questionNames := make([]string, 0, len(questions))
	for questionName := range questions {
		questionNames = append(questionNames, questionName)
	}
	sort.Strings(questionNames)
	return questionNames
}

func answerDocuments(answers map[string]model.DecisionAnswer) map[string]decisionAnswerDocument {
	documents := map[string]decisionAnswerDocument{}
	for questionName, answer := range answers {
		documents[questionName] = decisionAnswerDocument{
			Choice:        answer.Choice,
			Noul:          answer.Noul,
			Probabilities: answer.Probabilities,
			Confidence:    answer.Confidence,
		}
	}
	return documents
}
