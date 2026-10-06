package officeclaimcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

type scriptedDecisionModel struct {
	requests []model.DecisionRequest
	kindOf   func(text string) (string, map[string]float64)
}

func (decisionModel *scriptedDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.requests = append(decisionModel.requests, request)
	claims := request.State.(map[string]any)["claims"].(map[string]shownClaim)
	answers := map[string]model.DecisionAnswer{}
	for key := range request.Questions {
		kind, probabilities := decisionModel.kindOf(claims[key].Text)
		answers[key] = model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: kind, Probabilities: probabilities}
	}
	return model.DecisionResponse{Answers: answers, Usage: model.Usage{PromptTokens: 100, TotalTokens: 110}}, nil
}

func claimsOf(texts ...string) []Claim {
	claims := []Claim{}
	for index, text := range texts {
		claims = append(claims, Claim{Path: fmt.Sprintf("paragraphs[%d]", index), Text: text})
	}
	return claims
}

func flaggingInventions(text string) (string, map[string]float64) {
	switch {
	case strings.Contains(text, "invented"):
		return KindClaim, map[string]float64{KindClaim: 0.9}
	case strings.Contains(text, "wrong"):
		return KindMistake, map[string]float64{KindMistake: 0.8, KindSource: 0.2}
	case strings.Contains(text, "miscounted"):
		return KindError, map[string]float64{KindError: 0.7}
	case strings.Contains(text, "supercharge"):
		return KindHollow, map[string]float64{KindHollow: 0.9}
	}
	return KindSource, map[string]float64{KindSource: 0.95, KindClaim: 0.05}
}

func TestAClaimCopiedFromTheSourcesIsNotAsked(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	sources := Sources{
		Request:      []string{"Write a notice that the office  moves on 10/20."},
		Attachments:  []Attachment{{Name: "memo.txt", Text: "New address: 12 Sample Street"}},
		RuntimeFacts: json.RawMessage(`{"company":{"name":"Sample Co"}}`),
	}
	judgment, errorValue := Judge(context.Background(), decisionModel, sources, claimsOf("the office moves on 10/20.", "12 Sample Street", "Sample Co", "an invented promise"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(decisionModel.requests) != 1 || len(decisionModel.requests[0].Questions) != 1 {
		t.Fatalf("expected one call asking only the uncopied claim, got %+v", decisionModel.requests)
	}
	copied := 0
	for _, verdict := range judgment.Verdicts {
		if verdict.IsCopied {
			copied++
		}
	}
	if copied != 3 {
		t.Fatalf("expected three copied claims, got %d", copied)
	}
}

func TestOnlyAClaimAtOrAboveTheThresholdIsUnsupported(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	judgment, errorValue := Judge(context.Background(), decisionModel, Sources{Request: []string{"Write a notice."}}, claimsOf("Thank you for your support", "an invented commitment"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	unsupported := judgment.Flagged()
	if len(unsupported) != 1 || unsupported[0].Path != "paragraphs[1]" || unsupported[0].Defect != KindClaim {
		t.Fatalf("expected only the invented commitment, got %+v", unsupported)
	}
	if judgment.Usage.TotalTokens != 110 {
		t.Fatalf("expected the call's usage, got %+v", judgment.Usage)
	}
}

func TestEveryQuestionAsksTheSameKindsAndTheStateHoldsTheirMeaning(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	_, errorValue := Judge(context.Background(), decisionModel, Sources{Request: []string{"Write a notice."}}, claimsOf("first", "second"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	request := decisionModel.requests[0]
	if request.State.(map[string]any)["kinds"] == nil {
		t.Fatal("expected the kinds to be defined once in the state")
	}
	for key, question := range request.Questions {
		if question.Type != model.DecisionQuestionTypeChoice || len(question.Criteria.(map[string]string)) != len(CompactProfile.Kinds) {
			t.Fatalf("%s: expected a choice among every kind, got %+v", key, question)
		}
		if !strings.Contains(question.Instructions, "claims."+key) {
			t.Fatalf("%s: expected the question to name its own claim, got %q", key, question.Instructions)
		}
	}
}

func TestManyClaimsAreSplitIntoBoundedCalls(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	texts := []string{}
	for index := 0; index < claimsPerCall+1; index++ {
		texts = append(texts, fmt.Sprintf("value %d", index))
	}
	judgment, errorValue := Judge(context.Background(), decisionModel, Sources{Request: []string{"Write a report."}}, claimsOf(texts...))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(decisionModel.requests) != 2 || len(judgment.Verdicts) != claimsPerCall+1 {
		t.Fatalf("expected two calls covering every claim, got %d calls and %d verdicts", len(decisionModel.requests), len(judgment.Verdicts))
	}
}

func TestAnAttachmentIsShownUpToItsBound(t *testing.T) {
	long := strings.Repeat("가", attachmentMaxRunes+10)
	bounded := boundedAttachments([]Attachment{{Name: "long.txt", Text: long}, {Name: "empty.txt", Text: "  "}})
	if len(bounded) != 1 || len([]rune(bounded[0].Text)) != attachmentMaxRunes {
		t.Fatalf("expected one attachment cut to %d runes, got %+v", attachmentMaxRunes, len(bounded))
	}
}

type silentDecisionModel struct{}

func (silentDecisionModel) Decide(context.Context, model.DecisionRequest) (model.DecisionResponse, error) {
	return model.DecisionResponse{Answers: map[string]model.DecisionAnswer{}}, nil
}

func TestAMissingAnswerIsAnError(t *testing.T) {
	_, errorValue := Judge(context.Background(), silentDecisionModel{}, Sources{Request: []string{"Write a notice."}}, claimsOf("a value"))
	if errorValue == nil {
		t.Fatal("expected a missing answer to fail the check")
	}
}

func TestEachDefectKindIsRoutedToItsTreatment(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	texts := []string{"an invented fact", "a wrong date", "a miscounted total", "supercharge your day", "Thank you"}
	judgment, errorValue := Judge(context.Background(), decisionModel, Sources{Request: []string{"Write a notice."}}, claimsOf(texts...))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	blanked, rewritten := judgment.Treated(TreatmentBlank), judgment.Treated(TreatmentRewrite)
	if len(blanked) != 3 || len(rewritten) != 1 || rewritten[0].Defect != KindHollow {
		t.Fatalf("expected three blanked and the hollow rewritten, got %+v and %+v", blanked, rewritten)
	}
}

func TestADefectSplitAcrossKindsIsFlaggedAsTheStrongest(t *testing.T) {
	probabilities := map[string]float64{KindMistake: 0.3, KindClaim: 0.4, KindSource: 0.3}
	verdict := CompactProfile.VerdictFor(Claim{Text: "split"}, KindClaim, probabilities)
	if verdict.Defect != KindClaim || verdict.Treatment != TreatmentBlank {
		t.Fatalf("expected the strongest defect kind once the defects together reach the threshold, got %+v", verdict)
	}
	probabilities = map[string]float64{KindMistake: 0.2, KindClaim: 0.2, KindSource: 0.6}
	if verdict := CompactProfile.VerdictFor(Claim{Text: "fine"}, KindSource, probabilities); verdict.Defect != "" {
		t.Fatalf("expected a claim below the threshold to pass, got %+v", verdict)
	}
}

func TestTodayProfileKnowsOnlyTheOldKinds(t *testing.T) {
	if len(TodayProfile.Kinds) != 4 || len(TodayProfile.Treatments) != 1 {
		t.Fatalf("expected the four old kinds and one blanking defect, got %+v", TodayProfile)
	}
}

func TestEveryDefectKindIsDefinedInItsProfile(t *testing.T) {
	for _, profile := range []Profile{TodayProfile, CompactProfile} {
		for kind := range profile.Treatments {
			if profile.Kinds[kind] == "" {
				t.Fatalf("%s: defect kind %s has no definition", profile.Name, kind)
			}
		}
	}
}

func TestTheHollowDefinitionNamesItsThreeCriteriaAndItsBoundaries(t *testing.T) {
	definition := CompactProfile.Kinds[KindHollow]
	for _, part := range []string{"interchangeable", "functionless", "uncheckable weight", "more trustworthy and faster", "colder or impolite", "expression", "claim", "mistake", "Never choose hollow"} {
		if !strings.Contains(definition, part) {
			t.Fatalf("expected the hollow definition to hold %q, got %q", part, definition)
		}
	}
}

func TestWithdrawnValuesAreShownToTheJudgeAsRemovedAndOnlyWhenThereAreSome(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	if _, errorValue := Judge(context.Background(), decisionModel, Sources{Request: []string{"Write a notice."}}, claimsOf("first")); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, hasRemoved := decisionModel.requests[0].State.(map[string]any)["removed"]; hasRemoved {
		t.Fatal("a judgment with nothing removed showed a removed list")
	}
	sources := Sources{Request: []string{"Write a notice."}, Removed: []Claim{{Path: "slides[0].units[1]", At: "slide 1", Text: "Sign the lease"}}}
	if _, errorValue := Judge(context.Background(), decisionModel, sources, claimsOf("Two decisions today")); errorValue != nil {
		t.Fatal(errorValue)
	}
	removed, isShown := decisionModel.requests[1].State.(map[string]any)["removed"].([]shownClaim)
	if !isShown || len(removed) != 1 || removed[0].Text != "Sign the lease" || removed[0].At != "slide 1" {
		t.Fatalf("removed shown as %+v", decisionModel.requests[1].State.(map[string]any)["removed"])
	}
}

func TestAStatementThatCountsOrNamesRemovedValuesIsDefinedAsAClaim(t *testing.T) {
	for _, profile := range []Profile{TodayProfile, CompactProfile} {
		if !strings.Contains(profile.Kinds[KindClaim], "removed") {
			t.Fatalf("%s: the claim kind does not mention removed values: %q", profile.Name, profile.Kinds[KindClaim])
		}
	}
	if !strings.Contains(about, "removed") {
		t.Fatalf("the preface does not explain removed: %q", about)
	}
}

func TestAnOriginOrTaglineTheSourcesDoNotGiveIsDefinedAsAClaimAndANamedOneAsSource(t *testing.T) {
	for _, profile := range []Profile{TodayProfile, CompactProfile} {
		for _, part := range []string{"named as where a figure comes from", "even when the figure itself is in the sources", "tagline"} {
			if !strings.Contains(profile.Kinds[KindClaim], part) {
				t.Fatalf("%s: expected the claim kind to hold %q, got %q", profile.Name, part, profile.Kinds[KindClaim])
			}
		}
		if !strings.Contains(profile.Guard, "credit to an origin") || !strings.Contains(profile.Guard, "without crediting an origin is not claim") {
			t.Fatalf("%s: the question does not judge a credit to an origin on its own: %q", profile.Name, profile.Guard)
		}
		if !strings.Contains(profile.Kinds[KindSource], "an origin the sources name as its origin") {
			t.Fatalf("%s: the source kind does not keep an origin the sources name: %q", profile.Name, profile.Kinds[KindSource])
		}
	}
}
