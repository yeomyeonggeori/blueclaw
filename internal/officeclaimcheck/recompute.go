package officeclaimcheck

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

const recomputeSchema = `{"type":"object","properties":{"checks":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},"working":{"type":"string","description":"the recomputation from the sources' values, step by step, ending in your result"},"isWrong":{"type":"boolean","description":"true only when your result differs from the unit's stated value"}},"required":["key","working","isWrong"],"additionalProperties":false}}},"required":["checks"],"additionalProperties":false}`

type recomputeCheck struct {
	Key     string `json:"key"`
	Working string `json:"working"`
	IsWrong bool   `json:"isWrong"`
}

type recomputeAnswer struct {
	Checks []recomputeCheck `json:"checks"`
}

func (answer recomputeAnswer) wrongKeys() []string {
	keys := []string{}
	for _, check := range answer.Checks {
		if check.IsWrong {
			keys = append(keys, check.Key)
		}
	}
	return keys
}

func Recompute(ctx context.Context, profile Profile, writer model.LanguageModelProvider, sources Sources, judgment Judgment) (Judgment, error) {
	derived := derivedUnits(judgment)
	if len(derived) == 0 {
		return judgment, nil
	}
	var answer recomputeAnswer
	usage, errorValue := askStructured(ctx, writer, recomputeSchema, recomputePrompt(sources, derived), &answer)
	if errorValue != nil {
		return Judgment{}, fmt.Errorf("recompute: %w", errorValue)
	}
	rechecked := judgment.withWrongDerivations(profile, derived, answer.wrongKeys())
	rechecked.Usage = addedUsage(rechecked.Usage, usage)
	return rechecked, nil
}

func derivedUnits(judgment Judgment) map[string]Verdict {
	derived := map[string]Verdict{}
	for index, verdict := range judgment.Verdicts {
		if verdict.Kind == KindDerived && verdict.Defect == "" && !verdict.IsCopied {
			derived[fmt.Sprintf("claim%d", index)] = verdict
		}
	}
	return derived
}

func recomputePrompt(sources Sources, derived map[string]Verdict) string {
	shown := map[string]shownClaim{}
	for key, verdict := range derived {
		shown[key] = shownClaim{At: verdict.At, Text: verdict.Text}
	}
	state := batchState(CompactProfile, sources, nil)
	delete(state, "claims")
	delete(state, "kinds")
	sourcesText, _ := json.Marshal(state)
	claimsText, _ := json.Marshal(shown)
	return "Each unit below was written into a business document as following from the sources by arithmetic, the calendar or plain reasoning. " +
		"Recompute each one from the values the sources give, step by step in your head, and mark a unit wrong only when your result differs from its stated figure, date or conclusion. " +
		"Do not list a unit because it is brief, incomplete or worded differently, or because you cannot find its inputs. " +
		"Check every unit in checks, with your working before your verdict.\nSources: " + string(sourcesText) + "\nUnits: " + string(claimsText)
}

func (judgment Judgment) withWrongDerivations(profile Profile, derived map[string]Verdict, wrong []string) Judgment {
	flagged := map[string]bool{}
	for _, key := range wrong {
		flagged[key] = true
	}
	result := Judgment{Verdicts: append([]Verdict{}, judgment.Verdicts...), Usage: judgment.Usage}
	for index := range result.Verdicts {
		if flagged[fmt.Sprintf("claim%d", index)] && derived[fmt.Sprintf("claim%d", index)].Text == result.Verdicts[index].Text {
			result.Verdicts[index].Defect = KindError
			result.Verdicts[index].Treatment = profile.Treatments[KindError]
		}
	}
	return result
}
