package officevisualcheck

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

type Fixed struct {
	Number int    `json:"number"`
	Change string `json:"change"`
}

type SlideReport struct {
	Number   int       `json:"number"`
	Findings []Finding `json:"findings,omitempty"`
	Measured []string  `json:"measured,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type Usage struct {
	Decision model.Usage `json:"decision"`
	Language model.Usage `json:"language"`
}

type Report struct {
	RoundsUsed  int           `json:"roundsUsed"`
	Slides      []SlideReport `json:"slides"`
	Fixed       []Fixed       `json:"fixed,omitempty"`
	DeckFlagged []SlideReport `json:"deckFlagged,omitempty"`
	DeckError   string        `json:"deckError,omitempty"`
	GivenUp     []int         `json:"givenUp,omitempty"`
	Leftovers   []int         `json:"leftovers,omitempty"`
	TextChanged []int         `json:"textChanged,omitempty"`
	Usage       Usage         `json:"usage"`
}

type loopState struct {
	decisionModel model.DecisionModel
	languageModel model.LanguageModelProvider
	deck          Deck
	manifest      Manifest
	firstSections map[int]string
	assessments   map[int]Assessment
	fixProblems   map[int]string
	changes       map[int]string
	givenUp       map[int]bool
	refusals      map[int]string
	recompose     map[int]bool
	roundsUsed    int
	usage         Usage
	deckSheet     model.DecisionImage
	deckFlagged   []SlideReport
	deckError     string
}

func Run(ctx context.Context, decisionModel model.DecisionModel, languageModel model.LanguageModelProvider, deck Deck) (Report, error) {
	manifest, errorValue := deck.Manifest(ctx)
	if errorValue != nil {
		return Report{}, fmt.Errorf("read the visual review manifest: %w", errorValue)
	}
	state := &loopState{
		decisionModel: decisionModel,
		languageModel: languageModel,
		deck:          deck,
		manifest:      manifest,
		firstSections: manifest.sectionsByNumber(),
		assessments:   map[int]Assessment{},
		fixProblems:   map[int]string{},
		changes:       map[int]string{},
		givenUp:       map[int]bool{},
		refusals:      map[int]string{},
		recompose:     manifest.slidesToRecompose(),
	}
	state.recordReviews(reviewSlides(ctx, decisionModel, deck, manifest, manifest.Slides))
	state.reviewDeck(ctx)
	for state.roundsUsed < manifest.Rounds && len(state.candidates()) > 0 {
		if errorValue := state.fixRound(ctx); errorValue != nil {
			return state.report(), errorValue
		}
	}
	return state.report(), nil
}

func (state *loopState) recordReviews(reviews map[int]Assessment) {
	for number, assessment := range reviews {
		state.usage.Decision = addedUsage(state.usage.Decision, assessment.usage)
		state.assessments[number] = assessment
	}
}

func (state *loopState) candidates() []Slide {
	candidates := []Slide{}
	for _, slide := range state.manifest.Slides {
		if (state.assessments[slide.Number].IsFlagged() || state.recompose[slide.Number]) && !state.givenUp[slide.Number] {
			candidates = append(candidates, slide)
		}
	}
	return candidates
}

func (state *loopState) fixRound(ctx context.Context) error {
	state.roundsUsed++
	candidates := state.candidates()
	attempts := inParallel(candidates, func(slide Slide) attempt {
		return fixSlide(ctx, state.languageModel, state.deck, state.manifest.Fixer, slide, state.assessments[slide.Number], state.sheetFor(slide.Number), state.refusals[slide.Number])
	})
	replacements := state.acceptedReplacements(candidates, attempts)
	if len(replacements) == 0 {
		return nil
	}
	previous := state.manifest
	rebuilt, applied := state.rebuiltWith(ctx, replacements)
	if len(applied) == 0 {
		return nil
	}
	state.manifest = rebuilt
	reviews := reviewSlides(ctx, state.decisionModel, state.deck, rebuilt, rebuilt.slidesNumbered(applied))
	if len(state.deckSheet.Data) > 0 {
		reviews = state.reviewedAgainstDeck(ctx, rebuilt, reviews)
	}
	return state.settle(ctx, previous, attempts, candidates, reviews)
}

func (state *loopState) rebuiltWith(ctx context.Context, replacements map[int]string) (Manifest, map[int]string) {
	rebuilt, errorValue := state.deck.Rebuild(ctx, replacements)
	if errorValue == nil {
		return rebuilt, replacements
	}
	if len(replacements) == 1 {
		state.refuse(replacements, errorValue)
		return state.manifest, nil
	}
	applied := map[int]string{}
	rebuilt = state.manifest
	for _, number := range sortedNumbers(replacements) {
		single := map[int]string{number: replacements[number]}
		next, errorValue := state.deck.Rebuild(ctx, single)
		if errorValue != nil {
			state.refuse(single, errorValue)
			continue
		}
		rebuilt, applied[number] = next, replacements[number]
	}
	return rebuilt, applied
}

func (state *loopState) refuse(replacements map[int]string, errorValue error) {
	for number := range replacements {
		refusal := truncatedRunes(errorValue.Error(), maximumRefusalRunes)
		state.fixProblems[number] = "the deck rebuild refused the rewrite: " + refusal
		state.refusals[number] = refusal
	}
}

func truncatedRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func sortedNumbers(replacements map[int]string) []int {
	numbers := slices.Collect(maps.Keys(replacements))
	slices.Sort(numbers)
	return numbers
}

func (state *loopState) acceptedReplacements(candidates []Slide, attempts []attempt) map[int]string {
	replacements := map[int]string{}
	for index, slide := range candidates {
		state.usage.Language = addedUsage(state.usage.Language, attempts[index].usage)
		state.fixProblems[slide.Number] = attempts[index].problem
		if attempts[index].isAccepted() {
			replacements[slide.Number] = attempts[index].repair.Section
		}
	}
	return replacements
}

func (state *loopState) settle(ctx context.Context, previous Manifest, attempts []attempt, candidates []Slide, reviews map[int]Assessment) error {
	changes := map[int]string{}
	for index, slide := range candidates {
		changes[slide.Number] = attempts[index].repair.Change
	}
	restorations := map[int]string{}
	previousSections := previous.sectionsByNumber()
	for number, review := range reviews {
		state.usage.Decision = addedUsage(state.usage.Decision, review.usage)
		isRecomposed := state.recompose[number] && isNoWorse(state.assessments[number], review)
		delete(state.recompose, number)
		if isRecomposed || isImproved(state.assessments[number], review) {
			state.assessments[number] = review
			state.changes[number] = changes[number]
			continue
		}
		restorations[number] = previousSections[number]
		state.givenUp[number] = true
	}
	if len(restorations) == 0 {
		return nil
	}
	restored, errorValue := state.deck.Rebuild(ctx, restorations)
	if errorValue != nil {
		return fmt.Errorf("restore the slides whose fix did not help: %w", errorValue)
	}
	state.manifest = restored
	return nil
}

func isImproved(before Assessment, after Assessment) bool {
	if after.Error != "" || len(after.Measured) > len(before.Measured) {
		return false
	}
	if len(before.Findings) == 0 {
		return len(after.Measured) < len(before.Measured)
	}
	return !hasNewKind(before, after) && highestProbability(after.Probabilities, kindsOf(before)) < highestProbability(before.Probabilities, kindsOf(before))
}

func isNoWorse(before Assessment, after Assessment) bool {
	return after.Error == "" && len(after.Measured) <= len(before.Measured) && !hasNewKind(before, after)
}

func hasNewKind(before Assessment, after Assessment) bool {
	previousKinds := kindsOf(before)
	for _, kind := range kindsOf(after) {
		if !containsKind(previousKinds, kind) {
			return true
		}
	}
	return false
}

func kindsOf(assessment Assessment) []string {
	kinds := make([]string, 0, len(assessment.Findings))
	for _, finding := range assessment.Findings {
		kinds = append(kinds, finding.Kind)
	}
	return kinds
}

func containsKind(kinds []string, wanted string) bool {
	for _, kind := range kinds {
		if kind == wanted {
			return true
		}
	}
	return false
}

func highestProbability(probabilities map[string]float64, kinds []string) float64 {
	highest := 0.0
	for _, kind := range kinds {
		highest = max(highest, probabilities[kind])
	}
	return highest
}

func (state *loopState) report() Report {
	report := Report{RoundsUsed: state.roundsUsed, Usage: state.usage, DeckFlagged: state.deckFlagged, DeckError: state.deckError}
	for _, slide := range state.manifest.Slides {
		assessment := state.assessments[slide.Number]
		report.Slides = append(report.Slides, slideReport(slide.Number, assessment, state.fixProblems[slide.Number]))
		if assessment.IsFlagged() {
			report.Leftovers = append(report.Leftovers, slide.Number)
		}
		if state.givenUp[slide.Number] {
			report.GivenUp = append(report.GivenUp, slide.Number)
		}
		if change, isFixed := state.changes[slide.Number]; isFixed {
			report.Fixed = append(report.Fixed, Fixed{Number: slide.Number, Change: change})
		}
		if visibleText(state.firstSections[slide.Number]) != visibleText(slide.Section) {
			report.TextChanged = append(report.TextChanged, slide.Number)
		}
	}
	return report
}

func slideReport(number int, assessment Assessment, fixProblem string) SlideReport {
	codes := make([]string, 0, len(assessment.Measured))
	for _, defect := range assessment.Measured {
		codes = append(codes, defect.Code)
	}
	return SlideReport{Number: number, Findings: assessment.Findings, Measured: codes, Error: firstNonEmpty(assessment.Error, fixProblem)}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
