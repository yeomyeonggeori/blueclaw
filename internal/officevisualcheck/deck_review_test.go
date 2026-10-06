package officevisualcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

const (
	repetitiveOption   = "repetitive_layout"
	inconsistentOption = "inconsistent_style"
)

func deckQuestion() Question {
	return Question{
		Instructions: "Which pattern does this slide show across the deck?",
		Options:      map[string]string{cleanOption: "no pattern", repetitiveOption: "same composition as another slide", inconsistentOption: "styled unlike the rest"},
		CleanOption:  cleanOption,
	}
}

func manifestWithDeckQuestion(rounds int, sections ...string) Manifest {
	manifest := sampleManifest(rounds, sections...)
	manifest.Deck = deckQuestion()
	return manifest
}

type renderedDeck struct {
	*fakeDeck
	t     *testing.T
	large bool
}

func (deck *renderedDeck) Image(context.Context, string) ([]byte, error) {
	if deck.large {
		return noisyPNG(deck.t, 1600, 900), nil
	}
	return flatPNG(deck.t, 160, 90, color.Gray{Y: 120}), nil
}

type pairedDecisionModel struct {
	mutex        sync.Mutex
	slideRequest []model.DecisionRequest
	deckRequests []model.DecisionRequest
	deckProbs    func(number int) map[string]float64
	deckFailure  error
}

func (paired *pairedDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	paired.mutex.Lock()
	defer paired.mutex.Unlock()
	if _, isSlideQuestion := request.Questions[questionKey]; isSlideQuestion {
		paired.slideRequest = append(paired.slideRequest, request)
		return model.DecisionResponse{Answers: map[string]model.DecisionAnswer{questionKey: {Type: model.DecisionQuestionTypeChoice, Probabilities: map[string]float64{cleanOption: 0.95}}}}, nil
	}
	paired.deckRequests = append(paired.deckRequests, request)
	if paired.deckFailure != nil {
		return model.DecisionResponse{}, paired.deckFailure
	}
	answers := map[string]model.DecisionAnswer{}
	for name := range request.Questions {
		var number int
		fmt.Sscanf(name, "slide_%d", &number)
		answers[name] = model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Probabilities: paired.deckProbs(number)}
	}
	return model.DecisionResponse{Answers: answers, Usage: model.Usage{CostUSD: 0.25}}, nil
}

func repetitionOf(deck *fakeDeck) func(number int) map[string]float64 {
	return func(number int) map[string]float64 {
		if strings.Contains(deck.manifest.Slides[number-1].Section, "SAME") {
			return map[string]float64{cleanOption: 0.4, repetitiveOption: 0.6}
		}
		return map[string]float64{cleanOption: 0.97, repetitiveOption: 0.03}
	}
}

func varyingFixer() *fakeLanguageModel {
	return &fakeLanguageModel{rewrite: func(original string) Repair {
		return Repair{Section: strings.Replace(original, "SAME", "varied", 1), Change: "recomposed"}
	}}
}

func TestADeckQuestionIsAskedOnceWithOneSheetAndOneQuestionPerSlide(t *testing.T) {
	deck := &renderedDeck{fakeDeck: newFakeDeck(manifestWithDeckQuestion(0, section("a"), section("b"), section("c"))), t: t}
	decisions := &pairedDecisionModel{deckProbs: repetitionOf(deck.fakeDeck)}
	report, errorValue := Run(context.Background(), decisions, &fakeLanguageModel{}, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(decisions.deckRequests) != 1 || len(decisions.slideRequest) != 3 {
		t.Fatalf("%d deck requests, %d slide requests", len(decisions.deckRequests), len(decisions.slideRequest))
	}
	request := decisions.deckRequests[0]
	if len(request.Images) != 1 || len(request.Questions) != 3 {
		t.Fatalf("%d images, %d questions", len(request.Images), len(request.Questions))
	}
	wanted := model.ChoiceQuestion{Instructions: deckQuestion().Instructions + " Judge slide 2.", OptionDescriptions: deckQuestion().Options}.Question()
	encodedWanted, _ := json.Marshal(wanted)
	encodedGot, _ := json.Marshal(request.Questions["slide_2"])
	if string(encodedWanted) != string(encodedGot) {
		t.Fatalf("question %s, want %s", encodedGot, encodedWanted)
	}
	if len(report.Leftovers) != 0 || report.Usage.Decision.CostUSD != 0.25 {
		t.Fatalf("report %+v", report)
	}
}

func TestASlideTheDeckCallFlagsIsRecomposedWithTheSheetBesideIt(t *testing.T) {
	deck := &renderedDeck{fakeDeck: newFakeDeck(manifestWithDeckQuestion(1, section("one"), section("SAME two"), section("three"))), t: t}
	decisions := &pairedDecisionModel{deckProbs: repetitionOf(deck.fakeDeck)}
	fixer := varyingFixer()
	report, errorValue := Run(context.Background(), decisions, fixer, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(fixer.requests) != 1 {
		t.Fatalf("%d fixer requests", len(fixer.requests))
	}
	parts := fixer.requests[0].Messages[1].Parts
	if len(parts) != 3 || parts[2].Type != "image" {
		t.Fatalf("parts %+v", parts)
	}
	var payload repairContext
	json.Unmarshal([]byte(parts[0].Text), &payload)
	if len(payload.ReviewerFindings) != 1 || payload.ReviewerFindings[0].Kind != repetitiveOption || payload.ReviewerFindings[0].Meaning != "same composition as another slide" {
		t.Fatalf("findings %+v", payload.ReviewerFindings)
	}
	if len(decisions.deckRequests) != 2 || len(report.Leftovers) != 0 || len(report.Fixed) != 1 || report.Fixed[0].Number != 2 {
		t.Fatalf("%d deck requests, report %+v", len(decisions.deckRequests), report)
	}
	if len(report.DeckFlagged) != 1 || report.DeckFlagged[0].Number != 2 || report.DeckFlagged[0].Findings[0].Kind != repetitiveOption {
		t.Fatalf("deck flagged %+v", report.DeckFlagged)
	}
}

func TestASlideWithNoSheetBesideItIsFixedWithoutOne(t *testing.T) {
	deck := &renderedDeck{fakeDeck: newFakeDeck(manifestWithDeckQuestion(1, section("BAD"))), t: t}
	decisions := &pairedDecisionModel{deckProbs: repetitionOf(deck.fakeDeck)}
	slideModel := &fakeDecisionModel{answer: func(string) map[string]float64 {
		return map[string]float64{cleanOption: 0.5, "crowded": 0.5}
	}}
	routed := routedDecisions{slide: slideModel, deck: decisions}
	fixer := &fakeLanguageModel{rewrite: func(original string) Repair {
		return Repair{Section: strings.Replace(original, "BAD", "good", 1), Change: "regrouped"}
	}}
	if _, errorValue := Run(context.Background(), routed, fixer, deck); errorValue != nil {
		t.Fatal(errorValue)
	}
	if got := len(fixer.requests[0].Messages[1].Parts); got != 2 {
		t.Fatalf("%d parts in a per-slide repair", got)
	}
}

type routedDecisions struct {
	slide *fakeDecisionModel
	deck  *pairedDecisionModel
}

func (routed routedDecisions) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if _, isSlideQuestion := request.Questions[questionKey]; isSlideQuestion {
		return routed.slide.Decide(ctx, request)
	}
	return routed.deck.Decide(ctx, request)
}

func TestADeckFlagWithNoRoundsIsReportedAndNothingIsRewritten(t *testing.T) {
	deck := &renderedDeck{fakeDeck: newFakeDeck(manifestWithDeckQuestion(0, section("SAME one"), section("two"))), t: t}
	fixer := varyingFixer()
	report, errorValue := Run(context.Background(), &pairedDecisionModel{deckProbs: repetitionOf(deck.fakeDeck)}, fixer, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(fixer.requests) != 0 || !slices.Equal(report.Leftovers, []int{1}) || len(deck.rebuilds) != 0 {
		t.Fatalf("report %+v, %d fixer requests", report, len(fixer.requests))
	}
}

func TestARecompositionTheDeckCallStillFlagsIsRestored(t *testing.T) {
	deck := &renderedDeck{fakeDeck: newFakeDeck(manifestWithDeckQuestion(1, section("SAME one"), section("two"))), t: t}
	fixer := &fakeLanguageModel{rewrite: func(original string) Repair {
		return Repair{Section: appendedBody(original, "<p>shuffled</p>"), Change: "shuffled"}
	}}
	report, errorValue := Run(context.Background(), &pairedDecisionModel{deckProbs: repetitionOf(deck.fakeDeck)}, fixer, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if deck.manifest.Slides[0].Section != section("SAME one") || !slices.Equal(report.GivenUp, []int{1}) || !slices.Equal(report.Leftovers, []int{1}) {
		t.Fatalf("section %q, report %+v", deck.manifest.Slides[0].Section, report)
	}
}

func TestADeckCallThatFailsIsReportedAndTheRunStillFinishes(t *testing.T) {
	deck := &renderedDeck{fakeDeck: newFakeDeck(manifestWithDeckQuestion(1, section("a"))), t: t}
	decisions := &pairedDecisionModel{deckProbs: repetitionOf(deck.fakeDeck), deckFailure: errors.New("decisions endpoint answered 500")}
	report, errorValue := Run(context.Background(), decisions, &fakeLanguageModel{}, deck)
	if errorValue != nil || !strings.Contains(report.DeckError, "500") || len(report.Leftovers) != 0 {
		t.Fatalf("error %v, report %+v", errorValue, report)
	}
}

func TestAManifestWithoutADeckQuestionNeverBuildsASheet(t *testing.T) {
	deck := &renderedDeck{fakeDeck: newFakeDeck(sampleManifest(1, section("a"))), t: t}
	decisions := &pairedDecisionModel{deckProbs: repetitionOf(deck.fakeDeck)}
	if _, errorValue := Run(context.Background(), decisions, &fakeLanguageModel{}, deck); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(decisions.deckRequests) != 0 {
		t.Fatalf("%d deck requests", len(decisions.deckRequests))
	}
}

func TestEverySlideRenderIsShrunkToFitBeforeItIsSentToTheDecisionModel(t *testing.T) {
	deck := &renderedDeck{fakeDeck: newFakeDeck(manifestWithDeckQuestion(0, section("a"), section("b"))), t: t, large: true}
	decisions := &pairedDecisionModel{deckProbs: repetitionOf(deck.fakeDeck)}
	report, errorValue := Run(context.Background(), decisions, &fakeLanguageModel{}, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, request := range append(slices.Clone(decisions.slideRequest), decisions.deckRequests...) {
		for _, image := range request.Images {
			if len(image.Data) > maximumImageBytes {
				t.Fatalf("an image of %d bytes was sent", len(image.Data))
			}
		}
	}
	for _, slide := range report.Slides {
		if slide.Error != "" {
			t.Fatalf("slide %d: %s", slide.Number, slide.Error)
		}
	}
}

func TestAManifestsDeckQuestionIsValidated(t *testing.T) {
	build := func(change func(*Manifest)) error {
		manifest := manifestWithDeckQuestion(0, section("a"))
		change(&manifest)
		content, _ := json.Marshal(manifest)
		_, errorValue := ParseManifest(content)
		return errorValue
	}
	if errorValue := build(func(*Manifest) {}); errorValue != nil {
		t.Fatalf("a valid deck question was refused: %v", errorValue)
	}
	refused := map[string]func(*Manifest){
		"clean option missing": func(manifest *Manifest) { manifest.Deck.CleanOption = "nothing" },
		"option shared with the slide question": func(manifest *Manifest) {
			manifest.Deck.Options["crowded"] = "also here"
		},
	}
	for name, change := range refused {
		if errorValue := build(change); errorValue == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if errorValue := build(func(manifest *Manifest) { manifest.PatternThresholds = map[string]float64{repetitiveOption: 0.2} }); errorValue != nil {
		t.Errorf("a threshold for a deck option was refused: %v", errorValue)
	}
}

func TestTheDeckCallSharesTheManifestsFixRoundsWithTheSlideReview(t *testing.T) {
	deck := &renderedDeck{fakeDeck: newFakeDeck(manifestWithDeckQuestion(2, section("SAME start"))), t: t}
	reviews := 0
	decisions := routedDecisions{
		slide: &fakeDecisionModel{answer: func(string) map[string]float64 {
			reviews++
			return map[string]float64{cleanOption: 0.5, "crowded": 1 - 0.1*float64(reviews)}
		}},
		deck: &pairedDecisionModel{deckProbs: repetitionOf(deck.fakeDeck)},
	}
	fixer := &fakeLanguageModel{rewrite: func(original string) Repair {
		return Repair{Section: appendedBody(original, "+"), Change: "step"}
	}}
	report, errorValue := Run(context.Background(), decisions, fixer, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if report.RoundsUsed != 2 || len(fixer.requests) != 2 {
		t.Fatalf("rounds %d, %d fixer requests, report %+v", report.RoundsUsed, len(fixer.requests), report)
	}
	payload := repairContext{}
	json.Unmarshal([]byte(fixer.requests[0].Messages[1].Parts[0].Text), &payload)
	if !slices.ContainsFunc(payload.ReviewerFindings, func(finding Finding) bool { return finding.Kind == repetitiveOption }) {
		t.Fatalf("the first round did not carry the deck finding: %+v", payload.ReviewerFindings)
	}
}
