package inboundengagement

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type TaskFacts struct {
	Prompt         string `json:"prompt,omitempty"`
	Status         string `json:"status,omitempty"`
	Summary        string `json:"summary,omitempty"`
	PostedQuestion string `json:"postedQuestion,omitempty"`
}

type Facts struct {
	Messages         []Message
	ConversationType string
	VisibleContext   agentcontract.VisibleContext
	AgentIdentity    agentcontract.AgentIdentity
	Company          agentcontract.CompanyContext
	OpenTask         *TaskFacts
	FinishedTask     *TaskFacts
	Duties           []StandingDuty
	EnvironmentNow   time.Time
}

type Judgment struct {
	MessageID              string
	Addressing             AddressingDecision
	ReactionProbability    float64
	HasRelatesToActiveTask bool
	RelatesToActiveTask    bool
	BusyRoute              BusyRoute
}

type Decider interface {
	Decide(ctx context.Context, facts Facts, observe agentcontract.LLMCallObserver) ([]Judgment, error)
	FitsBurstBudget(facts Facts) bool
}

type DecisionModelDecider struct {
	decisionModel model.DecisionModel
	randomSource  func() float64
}

func NewDecisionModelDecider(decisionModel model.DecisionModel, randomSource func() float64) DecisionModelDecider {
	if randomSource == nil {
		randomSource = rand.Float64
	}
	return DecisionModelDecider{decisionModel: decisionModel, randomSource: randomSource}
}

func (decider DecisionModelDecider) Decide(ctx context.Context, facts Facts, observe agentcontract.LLMCallObserver) ([]Judgment, error) {
	if len(facts.Messages) == 0 {
		return nil, errors.New("a gateway decision needs at least one message")
	}
	request := newDecisionRequest(facts)
	if len(request.Questions) == 0 {
		return decider.readJudgments(facts, nil)
	}
	if decider.decisionModel == nil {
		return nil, errors.New("deciding where a message goes needs a decision model and none is configured")
	}
	startedAt := time.Now()
	response, errorValue := decider.decisionModel.Decide(ctx, request)
	if observe != nil {
		callRecord := agentcontract.DecisionCallRecord(request, response, time.Since(startedAt), errorValue)
		callRecord.DecidedMessageIDs = decidedMessageIDs(facts)
		observe(callRecord)
	}
	if errorValue != nil {
		return nil, errorValue
	}
	return decider.readJudgments(facts, response.Answers)
}

const burstRequestByteBudget = 80000

func (decider DecisionModelDecider) FitsBurstBudget(facts Facts) bool {
	document, errorValue := json.Marshal(newDecisionRequest(facts))
	return errorValue == nil && len(document) <= burstRequestByteBudget
}

func decidedMessageIDs(facts Facts) []string {
	messageIDs := make([]string, 0, len(facts.Messages))
	for _, message := range facts.Messages {
		messageIDs = append(messageIDs, strings.TrimSpace(message.MessageID))
	}
	return messageIDs
}

func (decider DecisionModelDecider) readJudgments(facts Facts, answers map[string]model.DecisionAnswer) ([]Judgment, error) {
	judgments := make([]Judgment, 0, len(facts.Messages))
	for index, message := range facts.Messages {
		judgment, errorValue := decider.readJudgment(facts, answerReader{answers: answers, messageKey: messageKey(index)}, message)
		if errorValue != nil {
			return nil, errorValue
		}
		judgments = append(judgments, judgment)
	}
	return judgments, nil
}

func (decider DecisionModelDecider) readJudgment(facts Facts, reader answerReader, message Message) (Judgment, error) {
	addressing, reactionProbability, errorValue := decider.readAddressing(reader, facts, message)
	if errorValue != nil {
		return Judgment{}, errorValue
	}
	judgment := Judgment{MessageID: strings.TrimSpace(message.MessageID), Addressing: addressing, ReactionProbability: reactionProbability}
	if relatesAnswer, isAnswered := reader.answers[reader.questionKey(QuestionRelatesToActiveTask)]; asksRelatesToActiveTask(facts) && isAnswered {
		judgment.HasRelatesToActiveTask = true
		judgment.RelatesToActiveTask = relatesAnswer.IsYes()
	}
	if asksBusyRoute(facts) {
		busyRoute, errorValue := reader.choice(QuestionBusyRoute)
		if errorValue != nil {
			return Judgment{}, errorValue
		}
		judgment.BusyRoute = BusyRoute(busyRoute)
	}
	return judgment, nil
}

func (decider DecisionModelDecider) readAddressing(reader answerReader, facts Facts, message Message) (AddressingDecision, float64, error) {
	target, errorValue := readTarget(reader, facts)
	if errorValue != nil {
		return AddressingDecision{}, 0, errorValue
	}
	shouldRespondAnswer, isAnswered := reader.answers[reader.questionKey(QuestionShouldRespond)]
	if !isAnswered {
		return AddressingDecision{}, 0, errors.New("the gateway decision is missing an answer for " + reader.questionKey(QuestionShouldRespond))
	}
	workAnswer, errorValue := reader.choiceAnswer(QuestionWork)
	if errorValue != nil {
		return AddressingDecision{}, 0, errorValue
	}
	addressing := AddressingDecision{Target: target, ShouldRespond: shouldRespondAnswer.IsYes(), HasWork: asksForWork(workAnswer)}
	reactionAnswer, errorValue := reader.choiceAnswer(QuestionReaction)
	if errorValue != nil {
		return AddressingDecision{}, 0, errorValue
	}
	reactionProbability := reactionAnswer.ChoiceProbability(ReactionOptionReact)
	if reactionProbability > reactionAnswer.ChoiceProbability(ReactionOptionNone) {
		emojiAnswer, errorValue := reader.choiceAnswer(QuestionReactionEmoji)
		if errorValue != nil {
			return AddressingDecision{}, 0, errorValue
		}
		addressing.ReactionEmoji = drawReactionEmoji(emojiAnswer, decider.randomSource())
	}
	if asksDuty(facts, message) {
		dutyAnswer, errorValue := reader.choiceAnswer(QuestionDuty)
		if errorValue != nil {
			return AddressingDecision{}, 0, errorValue
		}
		addressing = withDuty(addressing, dutyAnswer)
	}
	if addressing.Target == AddressingTargetHuman {
		addressing.ShouldRespond = false
	}
	return addressing, reactionProbability, nil
}

func withDuty(addressing AddressingDecision, dutyAnswer model.DecisionAnswer) AddressingDecision {
	duty, isDuty := StandingDutyByName(dutyAnswer.Choice)
	if !isDuty {
		return addressing
	}
	addressing.DutyMatch = true
	addressing.DutyName = duty.Name
	addressing.DutyConfidence = min(max(dutyAnswer.Confidence, 0), 1)
	return addressing
}

func readTarget(reader answerReader, facts Facts) (AddressingTarget, error) {
	if !asksTarget(facts) {
		return AddressingTargetBot, nil
	}
	target, errorValue := reader.choice(QuestionTarget)
	return AddressingTarget(target), errorValue
}

func asksForWork(workAnswer model.DecisionAnswer) bool {
	workWeight := 0.0
	for option, probability := range workAnswer.Probabilities {
		if option != WorkOptionNone {
			workWeight += probability
		}
	}
	return workWeight > workAnswer.ChoiceProbability(WorkOptionNone)
}

const reactionCandidateShareOfTheLikeliest = 0.5

func drawReactionEmoji(emojiAnswer model.DecisionAnswer, draw float64) string {
	candidates, totalWeight := reactionCandidates(emojiAnswer)
	if len(candidates) == 0 {
		return knownReactionEmoji(emojiAnswer.Choice)
	}
	threshold := draw * totalWeight
	for _, candidate := range candidates {
		threshold -= candidate.weight
		if threshold < 0 {
			return candidate.name
		}
	}
	return candidates[len(candidates)-1].name
}

type reactionCandidate struct {
	name   string
	weight float64
}

func reactionCandidates(emojiAnswer model.DecisionAnswer) ([]reactionCandidate, float64) {
	likeliest := 0.0
	for _, emoji := range reactionEmojis {
		likeliest = max(likeliest, emojiAnswer.ChoiceProbability(emoji.name))
	}
	candidates := []reactionCandidate{}
	totalWeight := 0.0
	for _, emoji := range reactionEmojis {
		weight := emojiAnswer.ChoiceProbability(emoji.name)
		if weight <= 0 || weight < likeliest*reactionCandidateShareOfTheLikeliest {
			continue
		}
		candidates = append(candidates, reactionCandidate{name: emoji.name, weight: weight})
		totalWeight += weight
	}
	return candidates, totalWeight
}

func knownReactionEmoji(name string) string {
	normalizedName := strings.ToLower(strings.TrimSpace(name))
	for _, emoji := range reactionEmojis {
		if emoji.name == normalizedName {
			return emoji.name
		}
	}
	return ""
}

type answerReader struct {
	answers    map[string]model.DecisionAnswer
	messageKey string
}

func (reader answerReader) questionKey(questionName string) string {
	return reader.messageKey + "." + questionName
}

func (reader answerReader) choiceAnswer(questionName string) (model.DecisionAnswer, error) {
	answer, isAnswered := reader.answers[reader.questionKey(questionName)]
	if !isAnswered || strings.TrimSpace(answer.Choice) == "" {
		return model.DecisionAnswer{}, errors.New("the gateway decision answered " + reader.questionKey(questionName) + " with no choice")
	}
	return answer, nil
}

func (reader answerReader) choice(questionName string) (string, error) {
	answer, errorValue := reader.choiceAnswer(questionName)
	return strings.TrimSpace(answer.Choice), errorValue
}
