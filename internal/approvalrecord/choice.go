package approvalrecord

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
)

const CancelChoiceKey = "cancel"

func ChoiceReplyOptions(choices []holdrecord.Choice) []agentcontract.ChoiceReplyOption {
	options := []agentcontract.ChoiceReplyOption{}
	for _, choice := range choices {
		options = append(options, agentcontract.ChoiceReplyOption{Key: strings.TrimSpace(choice.Key), Label: choiceReplyLabel(choice)})
	}
	return append(options, agentcontract.ChoiceReplyOption{Key: CancelChoiceKey, Label: "cancel, do not run it"})
}

func choiceReplyLabel(choice holdrecord.Choice) string {
	if choice.IsAnAnswer() {
		return strings.TrimSpace(choice.Label)
	}
	if choice.DefersTheCall() {
		return "run it at " + strings.TrimSpace(choice.StartsAt)
	}
	return "run it now"
}

func ChoiceByKey(choices []holdrecord.Choice, key string) (holdrecord.Choice, bool) {
	trimmedKey := strings.TrimSpace(key)
	for _, choice := range choices {
		if strings.TrimSpace(choice.Key) == trimmedKey && trimmedKey != "" {
			return choice, true
		}
	}
	return holdrecord.Choice{}, false
}

func OfferedChoices(taskEvents []agentcontract.TaskEvent) []holdrecord.Choice {
	holds := holdrecord.Holds(taskEvents)
	if len(holds) == 0 {
		return nil
	}
	return holds[len(holds)-1].Choices
}
