package officevisualcheck

import (
	"context"
	"fmt"
	"sort"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const deckQuestionPrefix = "slide_"

type deckReview struct {
	sheet       model.DecisionImage
	assessments map[int]Assessment
	usage       model.Usage
}

func reviewDeckAsAWhole(ctx context.Context, decisionModel model.DecisionModel, deck Deck, manifest Manifest) (deckReview, error) {
	renders, errorValue := slideRenders(ctx, deck, manifest.Slides)
	if errorValue != nil {
		return deckReview{}, errorValue
	}
	sheet, errorValue := contactSheet(renders)
	if errorValue != nil {
		return deckReview{}, errorValue
	}
	response, errorValue := decisionModel.Decide(ctx, deckRequest(manifest, sheet))
	if errorValue != nil {
		return deckReview{}, fmt.Errorf("decision model: %w", errorValue)
	}
	assessments, errorValue := deckAssessments(manifest, response)
	return deckReview{sheet: sheet, assessments: assessments, usage: response.Usage}, errorValue
}

func slideRenders(ctx context.Context, deck Deck, slides []Slide) ([][]byte, error) {
	renders := make([][]byte, len(slides))
	for index, slide := range slides {
		render, errorValue := deck.Image(ctx, slide.Image)
		if errorValue != nil {
			return nil, fmt.Errorf("read the render of slide %d: %w", slide.Number, errorValue)
		}
		renders[index] = render
	}
	return renders, nil
}

func deckRequest(manifest Manifest, sheet model.DecisionImage) model.DecisionRequest {
	questions := map[string]model.DecisionQuestion{}
	for _, slide := range manifest.Slides {
		instructions := fmt.Sprintf("%s Judge slide %d.", manifest.Deck.Instructions, slide.Number)
		questions[deckQuestionKey(slide.Number)] = model.ChoiceQuestion{Instructions: instructions, OptionDescriptions: manifest.Deck.Options}.Question()
	}
	state := map[string]any{
		"slides": len(manifest.Slides),
		"sheet":  "every slide of the deck at reduced size, in reading order from the top left; the number on each tile is the slide's number",
	}
	return model.DecisionRequest{State: state, Questions: questions, Images: []model.DecisionImage{sheet}}
}

func deckQuestionKey(number int) string {
	return fmt.Sprintf("%s%d", deckQuestionPrefix, number)
}

func deckAssessments(manifest Manifest, response model.DecisionResponse) (map[int]Assessment, error) {
	assessments := map[int]Assessment{}
	for _, slide := range manifest.Slides {
		answer, isAnswered := response.Answers[deckQuestionKey(slide.Number)]
		if !isAnswered {
			return nil, fmt.Errorf("the decision model gave no answer for slide %d of the deck", slide.Number)
		}
		assessments[slide.Number] = Assessment{Probabilities: answer.Probabilities, Findings: flaggedFindings(manifest, manifest.Deck, answer)}
	}
	return assessments, nil
}

func withDeckAssessment(assessment Assessment, deckAssessment Assessment) Assessment {
	probabilities := map[string]float64{}
	for kind, probability := range assessment.Probabilities {
		probabilities[kind] = probability
	}
	for kind, probability := range deckAssessment.Probabilities {
		probabilities[kind] = probability
	}
	assessment.Probabilities = probabilities
	assessment.Findings = sortedByProbability(append(append([]Finding{}, assessment.Findings...), deckAssessment.Findings...))
	return assessment
}

func sortedByProbability(findings []Finding) []Finding {
	sort.Slice(findings, func(left, right int) bool {
		if findings[left].Probability != findings[right].Probability {
			return findings[left].Probability > findings[right].Probability
		}
		return findings[left].Kind < findings[right].Kind
	})
	return findings
}

func (manifest Manifest) hasDeckFinding(assessment Assessment) bool {
	for _, finding := range assessment.Findings {
		if _, isDeckKind := manifest.Deck.Options[finding.Kind]; isDeckKind {
			return true
		}
	}
	return false
}

func (manifest Manifest) deckFlaggedSlides(reviews map[int]Assessment) []SlideReport {
	flagged := []SlideReport{}
	for _, slide := range manifest.Slides {
		if findings := deckFindingsOf(manifest, reviews[slide.Number]); len(findings) > 0 {
			flagged = append(flagged, SlideReport{Number: slide.Number, Findings: findings})
		}
	}
	return flagged
}

func deckFindingsOf(manifest Manifest, assessment Assessment) []Finding {
	findings := []Finding{}
	for _, finding := range assessment.Findings {
		if _, isDeckKind := manifest.Deck.Options[finding.Kind]; isDeckKind {
			findings = append(findings, finding)
		}
	}
	return findings
}

func (state *loopState) reviewDeck(ctx context.Context) {
	if !state.manifest.asksAboutTheDeck() {
		return
	}
	review, errorValue := reviewDeckAsAWhole(ctx, state.decisionModel, state.deck, state.manifest)
	state.usage.Decision = addedUsage(state.usage.Decision, review.usage)
	if errorValue != nil {
		state.deckError = errorValue.Error()
		return
	}
	state.deckSheet = review.sheet
	state.deckFlagged = state.manifest.deckFlaggedSlides(review.assessments)
	for _, flagged := range state.deckFlagged {
		state.assessments[flagged.Number] = withDeckAssessment(state.assessments[flagged.Number], review.assessments[flagged.Number])
	}
}

func (state *loopState) reviewedAgainstDeck(ctx context.Context, rebuilt Manifest, reviews map[int]Assessment) map[int]Assessment {
	review, errorValue := reviewDeckAsAWhole(ctx, state.decisionModel, state.deck, rebuilt)
	state.usage.Decision = addedUsage(state.usage.Decision, review.usage)
	for number, assessment := range reviews {
		if errorValue != nil {
			assessment.Error = "the deck review after the fix failed: " + errorValue.Error()
			reviews[number] = assessment
			continue
		}
		reviews[number] = withDeckAssessment(assessment, review.assessments[number])
	}
	return reviews
}

func (state *loopState) sheetFor(number int) model.DecisionImage {
	if state.manifest.hasDeckFinding(state.assessments[number]) {
		return state.deckSheet
	}
	return model.DecisionImage{}
}
