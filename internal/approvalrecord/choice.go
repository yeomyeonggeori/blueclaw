package approvalrecord

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const CancelChoiceKey = "cancel"

type Choice struct {
	Key      string `json:"key"`
	StartsAt string `json:"startsAt,omitempty"`
}

func (choice Choice) DefersTheCall() bool {
	return strings.TrimSpace(choice.StartsAt) != ""
}

func ChoiceReplyOptions(choices []Choice) []agentcontract.ChoiceReplyOption {
	options := []agentcontract.ChoiceReplyOption{}
	for _, choice := range choices {
		options = append(options, agentcontract.ChoiceReplyOption{Key: strings.TrimSpace(choice.Key), Label: choiceReplyLabel(choice)})
	}
	return append(options, agentcontract.ChoiceReplyOption{Key: CancelChoiceKey, Label: "cancel, do not run it"})
}

func choiceReplyLabel(choice Choice) string {
	if choice.DefersTheCall() {
		return "run it at " + strings.TrimSpace(choice.StartsAt)
	}
	return "run it now"
}

func ChoiceByKey(choices []Choice, key string) (Choice, bool) {
	trimmedKey := strings.TrimSpace(key)
	for _, choice := range choices {
		if strings.TrimSpace(choice.Key) == trimmedKey && trimmedKey != "" {
			return choice, true
		}
	}
	return Choice{}, false
}

func OfferedChoices(taskEvents []agentcontract.TaskEvent) []Choice {
	holds := Holds(taskEvents)
	if len(holds) == 0 {
		return nil
	}
	return holds[len(holds)-1].Choices
}
