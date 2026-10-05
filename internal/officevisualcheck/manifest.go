package officevisualcheck

import (
	"encoding/json"
	"fmt"
)

type Question struct {
	Instructions string            `json:"instructions"`
	Options      map[string]string `json:"options"`
	CleanOption  string            `json:"cleanOption"`
}

type Fixer struct {
	Instructions string `json:"instructions"`
}

type MeasuredDefect struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
}

type Slide struct {
	Number    int              `json:"number"`
	Image     string           `json:"image"`
	State     map[string]any   `json:"state"`
	Section   string           `json:"section"`
	Source    string           `json:"source,omitempty"`
	Recompose bool             `json:"recompose,omitempty"`
	Measured  []MeasuredDefect `json:"measured"`
}

type Manifest struct {
	Question          Question           `json:"question"`
	Deck              Question           `json:"deck,omitempty"`
	Threshold         float64            `json:"threshold"`
	PatternThresholds map[string]float64 `json:"patternThresholds,omitempty"`
	Rounds            int                `json:"rounds"`
	Fixer             Fixer              `json:"fixer"`
	Source            string             `json:"source"`
	Slides            []Slide            `json:"slides"`
}

func (manifest Manifest) thresholdFor(option string) float64 {
	if threshold, hasOwn := manifest.PatternThresholds[option]; hasOwn {
		return threshold
	}
	return manifest.Threshold
}

func (manifest Manifest) asksAboutTheDeck() bool {
	return len(manifest.Deck.Options) > 0
}

func (manifest Manifest) isDefectOption(option string) bool {
	return isDefectOf(manifest.Question, option) || isDefectOf(manifest.Deck, option)
}

func isDefectOf(question Question, option string) bool {
	_, isOption := question.Options[option]
	return isOption && option != question.CleanOption
}

func ParseManifest(content []byte) (Manifest, error) {
	var manifest Manifest
	if errorValue := json.Unmarshal(content, &manifest); errorValue != nil {
		return Manifest{}, fmt.Errorf("visual review manifest is not valid JSON: %w", errorValue)
	}
	if errorValue := manifest.validate(); errorValue != nil {
		return Manifest{}, errorValue
	}
	return manifest, nil
}

func (manifest Manifest) validate() error {
	if _, hasCleanOption := manifest.Question.Options[manifest.Question.CleanOption]; !hasCleanOption {
		return fmt.Errorf("visual review manifest: cleanOption %q is not one of the options", manifest.Question.CleanOption)
	}
	if manifest.Threshold <= 0 || manifest.Threshold >= 1 {
		return fmt.Errorf("visual review manifest: threshold %v is not between 0 and 1", manifest.Threshold)
	}
	if errorValue := manifest.validateDeckQuestion(); errorValue != nil {
		return errorValue
	}
	for option, threshold := range manifest.PatternThresholds {
		if !manifest.isDefectOption(option) {
			return fmt.Errorf("visual review manifest: patternThresholds names %q, which is not a defect option", option)
		}
		if threshold <= 0 || threshold >= 1 {
			return fmt.Errorf("visual review manifest: patternThresholds[%q] %v is not between 0 and 1", option, threshold)
		}
	}
	if manifest.Rounds < 0 {
		return fmt.Errorf("visual review manifest: rounds %d is negative", manifest.Rounds)
	}
	return nil
}

func (manifest Manifest) validateDeckQuestion() error {
	if !manifest.asksAboutTheDeck() {
		return nil
	}
	if _, hasCleanOption := manifest.Deck.Options[manifest.Deck.CleanOption]; !hasCleanOption {
		return fmt.Errorf("visual review manifest: the deck question's cleanOption %q is not one of its options", manifest.Deck.CleanOption)
	}
	for option := range manifest.Deck.Options {
		if option != manifest.Deck.CleanOption && isDefectOf(manifest.Question, option) {
			return fmt.Errorf("visual review manifest: option %q is a defect of both the slide question and the deck question", option)
		}
	}
	return nil
}

func (manifest Manifest) slidesNumbered(numbers map[int]string) []Slide {
	slides := []Slide{}
	for _, slide := range manifest.Slides {
		if _, isWanted := numbers[slide.Number]; isWanted {
			slides = append(slides, slide)
		}
	}
	return slides
}

func (manifest Manifest) sectionsByNumber() map[int]string {
	sections := map[int]string{}
	for _, slide := range manifest.Slides {
		sections[slide.Number] = slide.Section
	}
	return sections
}

func (manifest Manifest) PageSource(number int) string {
	slide, isFound := manifest.slideNumbered(number)
	if !isFound {
		return ""
	}
	return slide.Source
}

func (manifest Manifest) slidesToRecompose() map[int]bool {
	numbers := map[int]bool{}
	for _, slide := range manifest.Slides {
		if slide.Recompose {
			numbers[slide.Number] = true
		}
	}
	return numbers
}

func (manifest Manifest) slideNumbered(number int) (Slide, bool) {
	for _, slide := range manifest.Slides {
		if slide.Number == number {
			return slide, true
		}
	}
	return Slide{}, false
}
