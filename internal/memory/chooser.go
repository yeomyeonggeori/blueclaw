package memory

import (
	"context"
	"fmt"

	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

const choiceQuestionName = "relation"

// Chooser answers bluememo's judgements through the decision model, which
// returns a distribution over the answers rather than one of them, so the
// margin gates in bluememo's judge have something to gate on.
type Chooser struct {
	DecisionModel model.DecisionModel
}

func (chooser Chooser) Choose(ctx context.Context, request bluememo.ChoiceRequest) (map[string]float64, error) {
	optionDescriptions := make(map[string]string, len(request.Answers))
	for _, answer := range request.Answers {
		optionDescriptions[answer] = answer
	}
	choiceQuestion := model.ChoiceQuestion{Instructions: request.Instruction, OptionDescriptions: optionDescriptions}
	response, errorValue := chooser.DecisionModel.Decide(ctx, model.DecisionRequest{
		State:     request.Subject,
		Questions: map[string]model.DecisionQuestion{choiceQuestionName: choiceQuestion.Question()},
	})
	if errorValue != nil {
		return nil, errorValue
	}
	answer, isAnswered := response.Answers[choiceQuestionName]
	if !isAnswered {
		return nil, fmt.Errorf("the decision model answered no %q question", choiceQuestionName)
	}
	if len(answer.Probabilities) > 0 {
		return answer.Probabilities, nil
	}
	if answer.Choice == "" {
		return nil, fmt.Errorf("the decision model answered %q with neither a distribution nor a choice", choiceQuestionName)
	}
	return map[string]float64{answer.Choice: 1}, nil
}
