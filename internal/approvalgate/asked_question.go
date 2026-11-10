package approvalgate

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const TaskEventChoiceAnswered = "ask.choice_answered"

type askedQuestion struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
}

func AskedChoices(toolName string, toolInput json.RawMessage) []holdrecord.Choice {
	if strings.TrimSpace(toolName) != toolcontract.AskInputToolName {
		return nil
	}
	asked := askedQuestion{}
	if json.Unmarshal(toolInput, &asked) != nil {
		return nil
	}
	choices := []holdrecord.Choice{}
	for _, label := range asked.Choices {
		if strings.TrimSpace(label) != "" {
			choices = append(choices, holdrecord.Choice{Key: strconv.Itoa(len(choices) + 1), Label: strings.TrimSpace(label)})
		}
	}
	return choices
}

func AskedQuestion(toolInput json.RawMessage) string {
	asked := askedQuestion{}
	_ = json.Unmarshal(toolInput, &asked)
	return strings.TrimSpace(asked.Question)
}

func TakesOwnWords(choices []holdrecord.Choice) bool {
	for _, choice := range choices {
		if choice.IsAnAnswer() {
			return true
		}
	}
	return false
}

type choiceAnswered struct {
	Key      string `json:"key,omitempty"`
	OwnWords string `json:"ownWords,omitempty"`
}

func RecordChoiceAnswer(taskRunStore taskstate.TaskRunStore, taskRunID string, choice holdrecord.Choice) {
	if !choice.IsAnAnswer() {
		return
	}
	recordChoiceAnswered(taskRunStore, taskRunID, choiceAnswered{Key: strings.TrimSpace(choice.Key)})
}

func RecordOwnWordsAnswer(taskRunStore taskstate.TaskRunStore, taskRunID string, ownWords string) {
	if strings.TrimSpace(ownWords) == "" {
		return
	}
	recordChoiceAnswered(taskRunStore, taskRunID, choiceAnswered{OwnWords: strings.TrimSpace(ownWords)})
}

func recordChoiceAnswered(taskRunStore taskstate.TaskRunStore, taskRunID string, answered choiceAnswered) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	body, _ := json.Marshal(answered)
	taskRunStore.AppendTaskEvent(taskRunID, TaskEventChoiceAnswered, string(body))
}

type RequesterAnswer struct {
	ChoiceKey string
	Text      string
}

func AnswerChosen(taskEvents []agentcontract.TaskEvent, choices []holdrecord.Choice) (RequesterAnswer, bool) {
	answered, isAnswered := latestChoiceAnswered(taskEvents)
	if !isAnswered {
		return RequesterAnswer{}, false
	}
	if answered.OwnWords != "" {
		return RequesterAnswer{Text: answered.OwnWords}, true
	}
	choice, isChosen := ChoiceByKey(choices, answered.Key)
	return RequesterAnswer{ChoiceKey: choice.Key, Text: choice.Label}, isChosen
}

func latestChoiceAnswered(taskEvents []agentcontract.TaskEvent) (choiceAnswered, bool) {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if taskEvents[index].Name == agentcontract.TaskEventApprovalHoldOpened {
			return choiceAnswered{}, false
		}
		if taskEvents[index].Name != TaskEventChoiceAnswered {
			continue
		}
		answered := choiceAnswered{}
		return answered, json.Unmarshal([]byte(taskEvents[index].Body), &answered) == nil
	}
	return choiceAnswered{}, false
}

func ChosenAnswerResult(taskEvents []agentcontract.TaskEvent, toolInput json.RawMessage) toolcontract.ToolResult {
	answer, isAnswered := AnswerChosen(taskEvents, AskedChoices(toolcontract.AskInputToolName, toolInput))
	if !isAnswered {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, toolcontract.AskInputToolName, "a question with choices is answered by the requester picking one or answering in their own words, and neither happened")
	}
	data := map[string]string{"status": "answered", "question": AskedQuestion(toolInput), "answer": answer.Text}
	if answer.ChoiceKey == "" {
		document, _ := json.Marshal(data)
		return toolcontract.ToolSuccessData("The requester answered in their own words: "+answer.Text, document)
	}
	data["choiceKey"] = answer.ChoiceKey
	document, _ := json.Marshal(data)
	return toolcontract.ToolSuccessData("The requester chose: "+answer.Text, document)
}
