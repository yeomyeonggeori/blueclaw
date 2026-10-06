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

func RecordChoiceAnswer(taskRunStore taskstate.TaskRunStore, taskRunID string, choice holdrecord.Choice) {
	if strings.TrimSpace(taskRunID) == "" || !choice.IsAnAnswer() {
		return
	}
	body, _ := json.Marshal(map[string]string{"key": strings.TrimSpace(choice.Key)})
	taskRunStore.AppendTaskEvent(taskRunID, TaskEventChoiceAnswered, string(body))
}

func AnswerChosen(taskEvents []agentcontract.TaskEvent, choices []holdrecord.Choice) (holdrecord.Choice, bool) {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if taskEvents[index].Name == agentcontract.TaskEventApprovalHoldOpened {
			return holdrecord.Choice{}, false
		}
		if taskEvents[index].Name != TaskEventChoiceAnswered {
			continue
		}
		answered := struct {
			Key string `json:"key"`
		}{}
		if json.Unmarshal([]byte(taskEvents[index].Body), &answered) != nil {
			return holdrecord.Choice{}, false
		}
		return ChoiceByKey(choices, answered.Key)
	}
	return holdrecord.Choice{}, false
}

func ChosenAnswerResult(taskEvents []agentcontract.TaskEvent, toolInput json.RawMessage) toolcontract.ToolResult {
	chosen, isChosen := AnswerChosen(taskEvents, AskedChoices(toolcontract.AskInputToolName, toolInput))
	if !isChosen {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, toolcontract.AskInputToolName, "a question with choices is answered by the requester picking one, and none was picked")
	}
	answer, _ := json.Marshal(map[string]string{"status": "answered", "question": AskedQuestion(toolInput), "choiceKey": chosen.Key, "answer": chosen.Label})
	return toolcontract.ToolSuccessData("The requester chose: "+chosen.Label, answer)
}
