package turnoutcome

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const verdictSchemaDocument = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "outcome": {"type": "string", "enum": ["completed", "failed", "blocked"]},
    "reason": {"type": "string"}
  },
  "required": ["outcome", "reason"]
}`

const classifierInstruction = `You read the final message an agent left after working on a task, plus the list of tools it actually ran, and you decide what happened to the task.

Choose exactly one outcome:
- "completed": the message reports the requested work as done.
- "failed": the message reports that the work was not done, or reports an error, or answers something other than what was asked.
- "blocked": the message says the work cannot continue until a person supplies information, permission, or access.

The tool list is what the runtime observed, not what the agent claims. A message that reports work as done while no tool that could have performed it ran is "failed".

Judge only what the message reports. Do not invent requirements the task did not state. Wording, tone, formatting, length, and language are never grounds for "failed". An apologetic or hedged message that still reports the work as done is "completed".`

type Verdict struct {
	Status agentcontract.TaskStatus
	Reason string
}

type Classifier struct {
	languageModelProvider model.LanguageModelProvider
}

func NewClassifier(languageModelProvider model.LanguageModelProvider) Classifier {
	return Classifier{languageModelProvider: languageModelProvider}
}

func (classifier Classifier) IsConfigured() bool {
	return classifier.languageModelProvider != nil
}

func (classifier Classifier) Classify(ctx context.Context, prompt string, finishMessage string, calledToolNames []string) (Verdict, error) {
	if classifier.languageModelProvider == nil {
		return Verdict{}, errors.New("no outcome classifier is configured, so this harness cannot report whether a task succeeded")
	}
	response, errorValue := classifier.languageModelProvider.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: classifierInstruction},
			{Role: "user", Content: classificationSubject(prompt, finishMessage, calledToolNames)},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               "turn_outcome",
			Document:           verdictSchemaDocument,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return Verdict{}, errorValue
	}
	return parseVerdict(response.Content)
}

func classificationSubject(prompt string, finishMessage string, calledToolNames []string) string {
	toolSummary := "none"
	if len(calledToolNames) > 0 {
		toolSummary = strings.Join(calledToolNames, ", ")
	}
	return strings.Join([]string{
		"The task the person asked for:",
		strings.TrimSpace(prompt),
		"",
		"Tools the runtime observed the agent run:",
		toolSummary,
		"",
		"The agent's final message:",
		strings.TrimSpace(finishMessage),
	}, "\n")
}

func parseVerdict(responseContent string) (Verdict, error) {
	var parsed struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	}
	if errorValue := json.Unmarshal([]byte(responseContent), &parsed); errorValue != nil {
		return Verdict{}, errorValue
	}
	status, isKnown := statusForOutcome(parsed.Outcome)
	if !isKnown {
		return Verdict{}, errors.New("the outcome classifier answered with an unknown outcome " + parsed.Outcome)
	}
	return Verdict{Status: status, Reason: strings.TrimSpace(parsed.Reason)}, nil
}

func statusForOutcome(outcome string) (agentcontract.TaskStatus, bool) {
	switch strings.TrimSpace(outcome) {
	case "completed":
		return agentcontract.TaskStatusCompleted, true
	case "failed":
		return agentcontract.TaskStatusFailed, true
	case "blocked":
		return agentcontract.TaskStatusBlocked, true
	default:
		return "", false
	}
}
