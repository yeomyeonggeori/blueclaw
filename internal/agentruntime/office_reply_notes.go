package agentruntime

import (
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/claimcheck"
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
	flaggedVerdict := map[string]claimcheck.Verdict{}
	flaggedPlaces := []string{}
	for _, check := range checks {
		if check.Outcome != claimOutcomeBlanked {
			continue
		}
		for _, verdict := range check.Flagged {
			place := firstNonEmptyString(verdict.At, verdict.Path)
			if _, isKnown := flaggedVerdict[place]; !isKnown {
				flaggedPlaces = append(flaggedPlaces, place)
			}
			flaggedVerdict[place] = verdict
		}
	}
	descriptions := []string{}
	isDescribed := map[string]bool{}
	for _, place := range append(append([]string{}, labels...), flaggedPlaces...) {
		if isDescribed[place] || strings.TrimSpace(place) == "" {
			continue
		}
		isDescribed[place] = true
		if verdict, isFlagged := flaggedVerdict[place]; isFlagged {
			descriptions = append(descriptions, fmt.Sprintf("%s (it said %q, %s)", place, verdict.Text, flagReason(verdict.Defect)))
			continue
		}
		descriptions = append(descriptions, place)
	}
	return descriptions
}

func flagReason(defect string) string {
	switch defect {
	case claimcheck.KindMistake:
		return "which differs from what the person gave: ask them to confirm the right value"
	case claimcheck.KindError:
		return "which does not follow from what the person gave, or contradicts another part of the document: ask them to confirm"
	}
	return "which nothing the person gave supports"
}
