package inboundengagement

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type answeringModel struct {
	choices  map[string]string
	noul     map[string]float64
	requests []model.DecisionRequest
	failure  error
}

func (decisionModel *answeringModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.requests = append(decisionModel.requests, request)
	if decisionModel.failure != nil {
		return model.DecisionResponse{}, decisionModel.failure
	}
	answers := map[string]model.DecisionAnswer{}
	for questionKey, question := range request.Questions {
		answers[questionKey] = decisionModel.answerFor(questionKey, question)
	}
	return model.DecisionResponse{Answers: answers}, nil
}

func (decisionModel *answeringModel) answerFor(questionKey string, question model.DecisionQuestion) model.DecisionAnswer {
	questionName := questionKey[strings.Index(questionKey, ".")+1:]
	if question.Type == model.DecisionQuestionTypeNoul {
		return model.DecisionAnswer{Type: question.Type, Noul: decisionModel.noul[questionName]}
	}
	return model.DecisionAnswer{Type: question.Type, Choice: decisionModel.choices[questionName], Probabilities: map[string]float64{ReactionOptionReact: 0.9}}
}

func (decisionModel *answeringModel) askedQuestionNames() []string {
	names := []string{}
	for _, request := range decisionModel.requests {
		for questionKey := range request.Questions {
			names = append(names, questionKey)
		}
	}
	sort.Strings(names)
	return names
}

func defaultAnswers() *answeringModel {
	return &answeringModel{
		choices: map[string]string{
			QuestionTarget:        string(AddressingTargetBot),
			QuestionReaction:      ReactionOptionNone,
			QuestionReactionEmoji: "eyes",
			QuestionDuty:          DutyOptionNone,
			QuestionBusyRoute:     string(BusyRouteSteer),
		},
		noul: map[string]float64{QuestionShouldRespond: 1},
	}
}

func messageFacts(conversationType string, isMentioned bool, texts ...string) Facts {
	facts := Facts{ConversationType: conversationType, AgentIdentity: agentcontract.AgentIdentity{Name: "김인턴", Handle: "internkim"}}
	for _, text := range texts {
		facts.Messages = append(facts.Messages, Message{MessageID: "message-" + text, Prompt: text, SenderName: "이샘플", BotMentioned: isMentioned})
	}
	return facts
}

func withOpenTask(facts Facts, status string) Facts {
	facts.OpenTask = &TaskFacts{Prompt: "매출표를 만들어줘", Status: status, PostedQuestion: "보낼까요?"}
	return facts
}

func withFinishedTask(facts Facts) Facts {
	facts.FinishedTask = &TaskFacts{Prompt: "매출표를 만들어줘", Status: "completed"}
	return facts
}

func withDuties(facts Facts) Facts {
	facts.Duties = agentcontract.StandingDuties()
	return facts
}

func decideWith(t *testing.T, decisionModel *answeringModel, facts Facts) []Judgment {
	t.Helper()
	judgments, errorValue := NewDecisionModelDecider(decisionModel, func() float64 { return 0.5 }).Decide(context.Background(), facts, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return judgments
}

func TestADirectMessageWithNothingOpenMakesNoCall(t *testing.T) {
	decisionModel := defaultAnswers()

	judgments := decideWith(t, decisionModel, messageFacts("D", false, "내일 회의 잡아줘"))

	if len(decisionModel.requests) != 0 {
		t.Fatalf("expected no call, got %d", len(decisionModel.requests))
	}
	if len(judgments) != 1 || judgments[0].Addressing.Target != AddressingTargetBot || !judgments[0].Addressing.ShouldRespond {
		t.Fatalf("a direct message goes to the agent: %+v", judgments)
	}
}

func TestADirectMessageWithNothingOpenNeedsNoDecisionModel(t *testing.T) {
	judgments, errorValue := NewDecisionModelDecider(nil, nil).Decide(context.Background(), messageFacts("D", false, "안녕"), nil)

	if errorValue != nil || len(judgments) != 1 {
		t.Fatalf("expected one judgment and no error, got %+v: %v", judgments, errorValue)
	}
}

func TestAMessageNeedsAnAnswerOnlyToTheQuestionsItsFactsMakeRelevant(t *testing.T) {
	addressing := []string{"m1.reaction", "m1.reactionEmoji", "m1.shouldRespond", "m1.target"}
	openTask := []string{"m1.busyRoute", "m1.relatesToActiveTask"}
	testCases := []struct {
		name     string
		facts    Facts
		expected []string
	}{
		{"direct, a running task", withOpenTask(messageFacts("D", false, "a"), "running"), openTask},
		{"direct, a task waiting for approval", withOpenTask(messageFacts("D", false, "a"), "waiting_approval"), openTask},
		{"direct, a task waiting for an answer", withOpenTask(messageFacts("D", false, "a"), "waiting_user_input"), openTask},
		{"direct, a finished task", withFinishedTask(messageFacts("D", false, "a")), []string{"m1.relatesToActiveTask"}},
		{"channel, mentioned, nothing open", withDuties(messageFacts("O", true, "a")), addressing},
		{"channel, not mentioned, no duties", messageFacts("O", false, "a"), addressing},
		{"channel, not mentioned, duties", withDuties(messageFacts("O", false, "a")), append([]string{"m1.duty"}, addressing...)},
		{"channel, mentioned, a running task", withOpenTask(messageFacts("O", true, "a"), "running"), append(append([]string{}, addressing...), openTask...)},
		{"channel, mentioned, a finished task", withFinishedTask(messageFacts("O", true, "a")), append([]string{"m1.relatesToActiveTask"}, addressing...)},
		{"direct burst, a running task", withOpenTask(messageFacts("D", false, "a", "b"), "running"), []string{"m1.busyRoute", "m1.relatesToActiveTask", "m2.busyRoute", "m2.relatesToActiveTask"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			decisionModel := defaultAnswers()

			decideWith(t, decisionModel, testCase.facts)

			expected := append([]string{}, testCase.expected...)
			sort.Strings(expected)
			if actual := decisionModel.askedQuestionNames(); strings.Join(actual, ",") != strings.Join(expected, ",") {
				t.Fatalf("expected the questions %v, got %v", expected, actual)
			}
			if len(decisionModel.requests) != 1 {
				t.Fatalf("every question goes in one call, got %d", len(decisionModel.requests))
			}
		})
	}
}

func TestADutyIsAskedPerMessageWhenOnlySomeAreAddressedToTheAgent(t *testing.T) {
	facts := withDuties(messageFacts("O", false, "a", "b"))
	facts.Messages[1].BotMentioned = true
	decisionModel := defaultAnswers()

	decideWith(t, decisionModel, facts)

	asked := strings.Join(decisionModel.askedQuestionNames(), ",")
	if !strings.Contains(asked, "m1.duty") || strings.Contains(asked, "m2.duty") {
		t.Fatalf("expected a duty question for the unmentioned message only, got %s", asked)
	}
}

func TestTheStateCarriesTheOpenTaskItsStatusAndItsPostedQuestion(t *testing.T) {
	decisionModel := defaultAnswers()

	decideWith(t, decisionModel, withOpenTask(messageFacts("D", false, "삭제 말고 옮겨줘"), "waiting_approval"))

	document, _ := json.Marshal(decisionModel.requests[0].State)
	for _, fact := range []string{`"status":"waiting_approval"`, `"postedQuestion":"보낼까요?"`, `"placement":"direct"`, `"botMentioned":false`} {
		if !strings.Contains(string(document), fact) {
			t.Fatalf("the state lacks %s: %s", fact, document)
		}
	}
}

func TestTheStateHoldsFactsAndNoJudgment(t *testing.T) {
	facts := withFinishedTask(withDuties(messageFacts("O", false, "a")))
	decisionModel := defaultAnswers()

	decideWith(t, decisionModel, facts)

	document, _ := json.Marshal(decisionModel.requests[0].State)
	keys := map[string]json.RawMessage{}
	if errorValue := json.Unmarshal(document, &keys); errorValue != nil {
		t.Fatal(errorValue)
	}
	allowed := map[string]bool{"agent": true, "company": true, "placement": true, "now": true, "context": true, "messages": true, "standingDuties": true, "activeTask": true, "recentlyFinishedTask": true}
	for key := range keys {
		if !allowed[key] {
			t.Fatalf("the state holds %q, which is not a fact the gateway knows: %s", key, document)
		}
	}
}

func TestTheDutyCatalogReachesTheStateOnlyWhenADutyIsAsked(t *testing.T) {
	askedState := stateOf(t, withDuties(messageFacts("O", false, "a")))
	mentionedState := stateOf(t, withDuties(messageFacts("O", true, "a")))

	if !strings.Contains(askedState, "standingDuties") || strings.Contains(mentionedState, "standingDuties") {
		t.Fatalf("expected the catalog only in the asking state:\n%s\n%s", askedState, mentionedState)
	}
}

func stateOf(t *testing.T, facts Facts) string {
	t.Helper()
	decisionModel := defaultAnswers()
	decideWith(t, decisionModel, facts)
	document, _ := json.Marshal(decisionModel.requests[0].State)
	return string(document)
}

func TestTheBusyRouteOffersTheSameSixRoutesForARunningAndAWaitingTask(t *testing.T) {
	question := busyRouteQuestion("")

	criteria, isDescribed := question.Criteria.(map[string]string)
	if !isDescribed || len(criteria) != len(BusyRouteNames) {
		t.Fatalf("expected a description for each of %v, got %+v", BusyRouteNames, question.Criteria)
	}
	for _, routeName := range BusyRouteNames {
		if criteria[routeName] == "" {
			t.Fatalf("route %s has no description", routeName)
		}
	}
	if !strings.Contains(question.Instructions, "waiting_approval") || !strings.Contains(question.Instructions, "postedQuestion") {
		t.Fatalf("the wording does not read a waiting task: %s", question.Instructions)
	}
}

func TestAHumanTargetNeverGetsAReply(t *testing.T) {
	decisionModel := defaultAnswers()
	decisionModel.choices[QuestionTarget] = string(AddressingTargetHuman)

	judgments := decideWith(t, decisionModel, messageFacts("O", false, "박예시 님 감사합니다"))

	if judgments[0].Addressing.ShouldRespond {
		t.Fatalf("a message to another person is not answered: %+v", judgments[0])
	}
}

func TestAReactionEmojiIsReadOnlyWhenTheDrawFallsInsideTheProbability(t *testing.T) {
	facts := messageFacts("O", true, "감사합니다")

	reacting, _ := NewDecisionModelDecider(defaultAnswers(), func() float64 { return 0.1 }).Decide(context.Background(), facts, nil)
	silent, _ := NewDecisionModelDecider(defaultAnswers(), func() float64 { return 0.95 }).Decide(context.Background(), facts, nil)

	if reacting[0].Addressing.ReactionEmoji != "eyes" || silent[0].Addressing.ReactionEmoji != "" {
		t.Fatalf("expected eyes then nothing, got %q and %q", reacting[0].Addressing.ReactionEmoji, silent[0].Addressing.ReactionEmoji)
	}
	if reacting[0].ReactionProbability != 0.9 {
		t.Fatalf("the probability is reported for the caller, got %v", reacting[0].ReactionProbability)
	}
}

func TestADutyMatchCarriesItsNameAndBoundedConfidence(t *testing.T) {
	decisionModel := defaultAnswers()
	decisionModel.choices[QuestionDuty] = "calendar_upkeep"

	judgments := decideWith(t, decisionModel, withDuties(messageFacts("O", false, "내일 3시 회의")))

	addressing := judgments[0].Addressing
	if !addressing.DutyMatch || addressing.DutyName != "calendar_upkeep" || addressing.DutyConfidence != 0 {
		t.Fatalf("expected the calendar duty with the answered confidence, got %+v", addressing)
	}
}

func TestTheOpenTaskAnswersAreReadIntoTheJudgment(t *testing.T) {
	decisionModel := defaultAnswers()
	decisionModel.choices[QuestionBusyRoute] = string(BusyRouteStatus)
	decisionModel.noul[QuestionRelatesToActiveTask] = 1

	judgments := decideWith(t, decisionModel, withOpenTask(messageFacts("D", false, "아직이야?"), "waiting_approval"))

	judgment := judgments[0]
	if judgment.BusyRoute != BusyRouteStatus || !judgment.HasRelatesToActiveTask || !judgment.RelatesToActiveTask {
		t.Fatalf("expected status and a relation, got %+v", judgment)
	}
}

func TestAFinishedTaskAsksNoBusyRoute(t *testing.T) {
	judgments := decideWith(t, defaultAnswers(), withFinishedTask(messageFacts("D", false, "그 표 엑셀로도 줘")))

	if judgments[0].BusyRoute != "" || !judgments[0].HasRelatesToActiveTask {
		t.Fatalf("expected a relation and no route, got %+v", judgments[0])
	}
}

func TestAMissingAnswerIsAnError(t *testing.T) {
	decisionModel := defaultAnswers()
	decisionModel.choices[QuestionTarget] = ""

	_, errorValue := NewDecisionModelDecider(decisionModel, nil).Decide(context.Background(), messageFacts("O", true, "a"), nil)

	if errorValue == nil {
		t.Fatal("expected an error for an unanswered target")
	}
}

func TestADecisionModelFailureIsReturnedAndObserved(t *testing.T) {
	decisionModel := defaultAnswers()
	decisionModel.failure = errors.New("decisions endpoint answered 502")
	observedRecords := []agentcontract.LLMCallRecord{}

	_, errorValue := NewDecisionModelDecider(decisionModel, nil).Decide(context.Background(), messageFacts("O", true, "a"), func(record agentcontract.LLMCallRecord) { observedRecords = append(observedRecords, record) })

	if errorValue == nil || len(observedRecords) != 1 || !observedRecords[0].IsError {
		t.Fatalf("expected the failure and one error record, got %v and %+v", errorValue, observedRecords)
	}
}

func TestAChannelMessageWithoutADecisionModelIsAnError(t *testing.T) {
	_, errorValue := NewDecisionModelDecider(nil, nil).Decide(context.Background(), messageFacts("O", true, "a"), nil)

	if errorValue == nil {
		t.Fatal("expected an error when a call is needed and no model is configured")
	}
}

func TestACallRecordNamesEveryMessageItJudged(t *testing.T) {
	observedRecords := []agentcontract.LLMCallRecord{}

	_, errorValue := NewDecisionModelDecider(defaultAnswers(), nil).Decide(context.Background(), messageFacts("O", true, "a", "b"), func(record agentcontract.LLMCallRecord) { observedRecords = append(observedRecords, record) })

	if errorValue != nil || len(observedRecords) != 1 {
		t.Fatalf("expected one call record, got %+v: %v", observedRecords, errorValue)
	}
	if strings.Join(observedRecords[0].DecidedMessageIDs, ",") != "message-a,message-b" {
		t.Fatalf("expected the record to name both messages, got %v", observedRecords[0].DecidedMessageIDs)
	}
}

func TestABurstFitsTheBudgetUntilItsRequestOutgrowsIt(t *testing.T) {
	decider := NewDecisionModelDecider(defaultAnswers(), nil)

	if !decider.FitsBurstBudget(messageFacts("O", true, "short")) {
		t.Fatal("a short message should fit the burst budget")
	}
	if decider.FitsBurstBudget(messageFacts("O", true, strings.Repeat("a", burstRequestByteBudget))) {
		t.Fatal("a message as long as the whole budget should not fit")
	}
}

func TestOnlyARequestOfGatewayQuestionsIsAGatewayRequest(t *testing.T) {
	gatewayRequest := newDecisionRequest(withOpenTask(messageFacts("O", true, "a"), "running"))
	planningQuestions := map[string]model.DecisionQuestion{"m1." + agentcontract.IntakeQuestionRoute: model.ChoiceQuestion{}.Question()}
	for questionName, question := range gatewayRequest.Questions {
		planningQuestions[questionName] = question
	}

	if !AsksOnlyGatewayQuestions(gatewayRequest.Questions) {
		t.Fatalf("expected the decider's own questions to be gateway questions: %v", gatewayRequest.Questions)
	}
	if AsksOnlyGatewayQuestions(planningQuestions) {
		t.Fatal("a request that also asks for the route is a planning request")
	}
	if AsksOnlyGatewayQuestions(nil) {
		t.Fatal("a request with no questions is not a gateway request")
	}
}
