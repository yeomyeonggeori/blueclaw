package agentruntime

import (
	"fmt"
	"strings"
)

func deliveredFileNotes(filename string, blankLabels []string, checks []officeClaimCheck, review *officeVisualReview) []string {
	notes := []string{}
	if blanks := blankDescriptions(blankLabels, checks); len(blanks) > 0 {
		notes = append(notes, filename+": left blank, for the reply to offer to complete: "+strings.Join(blanks, ", "))
	}
	if review != nil && review.Outcome == visualOutcomeLeftovers {
		notes = append(notes, filename+": slides that still show a defect after the visual review, for the reply to say what remains: "+leftoverSlideNames(review.Leftovers))
	}
	return notes
}

func blankDescriptions(labels []string, checks []officeClaimCheck) []string {
	unsupportedText := map[string]string{}
	unsupportedPlaces := []string{}
	for _, check := range checks {
		if check.Outcome != claimOutcomeBlanked {
			continue
		}
		for _, verdict := range check.Unsupported {
			place := firstNonEmptyString(verdict.At, verdict.Path)
			if _, isKnown := unsupportedText[place]; !isKnown {
				unsupportedPlaces = append(unsupportedPlaces, place)
			}
			unsupportedText[place] = verdict.Text
		}
	}
	descriptions := []string{}
	isDescribed := map[string]bool{}
	for _, place := range append(append([]string{}, labels...), unsupportedPlaces...) {
		if isDescribed[place] || strings.TrimSpace(place) == "" {
			continue
		}
		isDescribed[place] = true
		if text, isUnsupported := unsupportedText[place]; isUnsupported {
			descriptions = append(descriptions, fmt.Sprintf("%s (it said %q, which nothing the person gave supports)", place, text))
			continue
		}
		descriptions = append(descriptions, place)
	}
	return descriptions
}
