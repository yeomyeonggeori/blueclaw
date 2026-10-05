package officeclaimcheck

import (
	"context"
	"testing"
)

func TestRecomputeFlagsOnlyTheDerivedUnitsTheWriterGotWrong(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: func(text string) (string, map[string]float64) {
		return KindDerived, map[string]float64{KindDerived: 0.95}
	}}
	writer := &scriptedWriter{rewrite: func(string) string {
		return `{"checks":[{"key":"claim0","working":"2x10=20","isWrong":false},{"key":"claim1","working":"2x10=20, not 30","isWrong":true}]}`
	}}
	sources := Sources{Request: []string{"Two items at 10 each."}}
	judgment, _ := Judge(context.Background(), decisionModel, sources, claimsOf("Total: 20", "Total: 30"))
	rechecked, errorValue := Recompute(context.Background(), CompactProfile, writer, sources, judgment)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	flagged := rechecked.Flagged()
	if len(flagged) != 1 || flagged[0].Text != "Total: 30" || flagged[0].Defect != KindError || flagged[0].Treatment != TreatmentBlank {
		t.Fatalf("expected the wrong total flagged as an error to blank, got %+v", flagged)
	}
	if len(writer.prompts) != 1 {
		t.Fatalf("expected one call for the whole document, got %d", len(writer.prompts))
	}
}

func TestRecomputeAsksNothingWhenNoUnitIsDerived(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	writer := &scriptedWriter{rewrite: func(string) string { return `{"checks":[]}` }}
	sources := Sources{Request: []string{"Write a notice."}}
	judgment, _ := Judge(context.Background(), decisionModel, sources, claimsOf("Thank you"))
	if _, errorValue := Recompute(context.Background(), CompactProfile, writer, sources, judgment); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(writer.prompts) != 0 {
		t.Fatalf("expected no call, got %d", len(writer.prompts))
	}
}

func TestRecomputeFailsLoudlyOnAnAnswerThatIsNotTheClosedShape(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: func(string) (string, map[string]float64) {
		return KindDerived, map[string]float64{KindDerived: 0.95}
	}}
	writer := &scriptedWriter{rewrite: func(string) string { return "it looks right" }}
	sources := Sources{Request: []string{"Two items at 10 each."}}
	judgment, _ := Judge(context.Background(), decisionModel, sources, claimsOf("Total: 20"))
	if _, errorValue := Recompute(context.Background(), CompactProfile, writer, sources, judgment); errorValue == nil {
		t.Fatal("expected an unreadable answer to be an error")
	}
}
