package officeclaimcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const rewriteSchema = `{"type":"object","properties":{"text":{"type":"string","description":"the rewritten unit, nothing else"}},"required":["text"],"additionalProperties":false}`

type rewriteAnswer struct {
	Text string `json:"text"`
}

func askStructured(ctx context.Context, writer model.LanguageModelProvider, schema string, prompt string, answer any) (model.Usage, error) {
	response, errorValue := writer.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages:               []model.Message{{Role: "user", Content: prompt}},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_claim_check", Document: schema, IsStrictlyEnforced: true},
	})
	if errorValue != nil {
		return model.Usage{}, errorValue
	}
	if errorValue := json.Unmarshal([]byte(response.Content), answer); errorValue != nil {
		return model.Usage{}, fmt.Errorf("the answer %q does not fit the schema: %w", response.Content, errorValue)
	}
	return response.Usage, nil
}

type Outcome struct {
	Blank    []Verdict `json:"blank,omitempty"`
	Replaced []Claim   `json:"replaced,omitempty"`
	Removed  []Verdict `json:"removed,omitempty"`
	Kept     []Verdict `json:"kept,omitempty"`
	Usage    model.Usage
}

func Treat(ctx context.Context, profile Profile, decisionModel model.DecisionModel, writer model.LanguageModelProvider, sources Sources, judgment Judgment) (Outcome, error) {
	outcome := Outcome{Blank: judgment.Treated(TreatmentBlank)}
	toRewrite := judgment.Treated(TreatmentRewrite)
	rewrites, rewritten := outcome.rewritten(ctx, profile, writer, sources, toRewrite)
	if len(rewrites) == 0 {
		return outcome, nil
	}
	rechecked, errorValue := JudgeWith(ctx, profile, decisionModel, sources, rewrites)
	if errorValue != nil {
		return Outcome{}, errorValue
	}
	outcome.Usage = addedUsage(outcome.Usage, rechecked.Usage)
	for index, verdict := range rechecked.Verdicts {
		outcome.place(rewritten[index], verdict)
	}
	return outcome, nil
}

func (outcome *Outcome) place(original Verdict, rewrite Verdict) {
	switch {
	case rewrite.Defect != "":
		outcome.Kept = append(outcome.Kept, original)
	case strings.TrimSpace(rewrite.Text) != "":
		outcome.Replaced = append(outcome.Replaced, rewrite.Claim)
	case original.IsFree:
		outcome.Removed = append(outcome.Removed, original)
	default:
		outcome.Kept = append(outcome.Kept, original)
	}
}

func (outcome *Outcome) rewritten(ctx context.Context, profile Profile, writer model.LanguageModelProvider, sources Sources, verdicts []Verdict) ([]Claim, []Verdict) {
	rewrites, rewritten := []Claim{}, []Verdict{}
	for _, verdict := range verdicts {
		var answer rewriteAnswer
		usage, errorValue := askStructured(ctx, writer, rewriteSchema, rewritePrompt(profile, sources, verdict), &answer)
		outcome.Usage = addedUsage(outcome.Usage, usage)
		if errorValue != nil {
			outcome.Kept = append(outcome.Kept, verdict)
			continue
		}
		rewrites = append(rewrites, Claim{Path: verdict.Path, At: verdict.At, Text: strings.TrimSpace(answer.Text), IsFree: verdict.IsFree})
		rewritten = append(rewritten, verdict)
	}
	return rewrites, rewritten
}

func rewritePrompt(profile Profile, sources Sources, verdict Verdict) string {
	state := batchState(profile, sources, nil)
	delete(state, "claims")
	delete(state, "kinds")
	encoded, _ := json.Marshal(state)
	return fmt.Sprintf("You rewrite one unit of a business document. %s\nSources: %s\nUnit: %s\nAnswer with the rewritten unit as the text field. Keep every fact, number and name the unit already has, and add none.",
		profile.Rewrites[verdict.Defect], encoded, verdict.Text)
}
