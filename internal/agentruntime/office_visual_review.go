package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/claimcheck"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/visualcheck"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const (
	officeVisualImageReadLimit    = 16 << 20
	officeVisualManifestReadLimit = 4 << 20
	officeVisualSourceReadLimit   = 8 << 20
	officeCommandPrefix           = "office"
)

const (
	visualOutcomeClean        = "clean"
	visualOutcomeFixed        = "fixed"
	visualOutcomeLeftovers    = "leftovers"
	visualOutcomeReviewFailed = "review_failed"
)

var slideSectionPattern = regexp.MustCompile(`(?is)<section[\s>].*?</section\s*>`)

type officeVisualLeftover struct {
	Number   int      `json:"number"`
	Findings []string `json:"findings,omitempty"`
	Measured []string `json:"measured,omitempty"`
}

type officeVisualReview struct {
	File            string                 `json:"file"`
	RoundsUsed      int                    `json:"roundsUsed"`
	Fixed           []visualcheck.Fixed    `json:"fixed,omitempty"`
	GivenUp         []int                  `json:"givenUp,omitempty"`
	Leftovers       []officeVisualLeftover `json:"leftovers,omitempty"`
	TextChanged     []int                  `json:"textChanged,omitempty"`
	RecheckedClaims int                    `json:"recheckedClaims"`
	CostUSD         float64                `json:"costUSD"`
	Outcome         string                 `json:"outcome"`
	Detail          string                 `json:"detail,omitempty"`

	claimRecheck *officeClaimCheck
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseVisualReviewModels(decisionModel model.DecisionModel, languageModel model.LanguageModelProvider) {
	toolCatalogBuilder.visualReviewDecisionModel = decisionModel
	toolCatalogBuilder.visualReviewLanguageModel = languageModel
}

func (toolCatalogBuilder *ToolCatalogBuilder) reviewsDeckRenders() bool {
	return toolCatalogBuilder.visualReviewDecisionModel != nil && toolCatalogBuilder.visualReviewLanguageModel != nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) checkOfficeVisualReview(ctx context.Context, request ToolCatalogRequest, concretePath string) *officeVisualReview {
	if !toolCatalogBuilder.reviewsDeckRenders() {
		return nil
	}
	actor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(ctx, request)
	if actorFailure != nil {
		return nil
	}
	if !toolCatalogBuilder.isWrittenInThisTask(ctx, actor, concretePath) {
		return nil
	}
	snapshot, isRead := readOfficeSnapshot(ctx, actor, concretePath)
	if !isRead || snapshot.VisualReview == "" {
		return nil
	}
	review := &officeVisualReview{File: filepath.Base(concretePath)}
	if errorValue := toolCatalogBuilder.reviewDeck(ctx, request, actor, concretePath, snapshot, review); errorValue != nil {
		review.Outcome, review.Detail = visualOutcomeReviewFailed, errorValue.Error()
	}
	return review
}

func (toolCatalogBuilder *ToolCatalogBuilder) reviewDeck(ctx context.Context, request ToolCatalogRequest, actor security.WorkspaceActor, concretePath string, before officeSnapshot, review *officeVisualReview) error {
	deck := &officeDeck{toolCatalogBuilder: toolCatalogBuilder, request: request, actor: actor, snapshot: before, concretePath: concretePath}
	report, errorValue := visualcheck.Run(ctx, toolCatalogBuilder.visualReviewDecisionModel, toolCatalogBuilder.visualReviewLanguageModel, deck)
	review.recordReport(report)
	if errorValue != nil {
		return errorValue
	}
	after, isRead := readOfficeSnapshot(ctx, actor, concretePath)
	if !isRead {
		return errors.New("the deck's snapshot cannot be read after the review")
	}
	if len(report.TextChanged) > 0 {
		toolCatalogBuilder.recheckChangedClaims(ctx, request, actor, concretePath, before, after, review)
	}
	return restoreOfficeBlanks(ctx, actor, concretePath, before.Blanks)
}

func (review *officeVisualReview) recordReport(report visualcheck.Report) {
	review.RoundsUsed = report.RoundsUsed
	review.Fixed = report.Fixed
	review.GivenUp = report.GivenUp
	review.TextChanged = report.TextChanged
	review.CostUSD = report.Usage.Decision.CostUSD + report.Usage.Language.CostUSD
	review.Leftovers = leftoversOf(report)
	review.Outcome = visualOutcomeOf(report)
}

func visualOutcomeOf(report visualcheck.Report) string {
	switch {
	case len(report.Leftovers) > 0:
		return visualOutcomeLeftovers
	case len(report.Fixed) > 0:
		return visualOutcomeFixed
	}
	return visualOutcomeClean
}

func leftoversOf(report visualcheck.Report) []officeVisualLeftover {
	leftovers := []officeVisualLeftover{}
	for _, slide := range report.Slides {
		if !slices.Contains(report.Leftovers, slide.Number) {
			continue
		}
		leftover := officeVisualLeftover{Number: slide.Number, Measured: slide.Measured}
		for _, finding := range slide.Findings {
			leftover.Findings = append(leftover.Findings, finding.Kind)
		}
		leftovers = append(leftovers, leftover)
	}
	return leftovers
}

func (toolCatalogBuilder *ToolCatalogBuilder) recheckChangedClaims(ctx context.Context, request ToolCatalogRequest, actor security.WorkspaceActor, concretePath string, before officeSnapshot, after officeSnapshot, review *officeVisualReview) {
	changed := newOrChangedClaims(before.Claims, after.Claims)
	review.RecheckedClaims = len(changed)
	if len(changed) == 0 || toolCatalogBuilder.claimDecisionModel == nil {
		return
	}
	review.claimRecheck = toolCatalogBuilder.judgeAndBlank(ctx, request, actor, concretePath, after, changed)
}

func newOrChangedClaims(before []claimcheck.Claim, after []claimcheck.Claim) []claimcheck.Claim {
	known := map[claimcheck.Claim]bool{}
	for _, claim := range before {
		known[claimcheck.Claim{Path: claim.Path, Text: claim.Text}] = true
	}
	changed := []claimcheck.Claim{}
	for _, claim := range after {
		if !known[claimcheck.Claim{Path: claim.Path, Text: claim.Text}] {
			changed = append(changed, claim)
		}
	}
	return changed
}

func restoreOfficeBlanks(ctx context.Context, actor security.WorkspaceActor, concretePath string, blanksBefore []officeBlank) error {
	if len(blanksBefore) == 0 {
		return nil
	}
	snapshotPath := concretePath + officeContract.SourceSuffix
	content, errorValue := actor.ReadFile(ctx, snapshotPath, officeSnapshotReadLimit)
	if errorValue != nil {
		return fmt.Errorf("read the snapshot to restore its blanks: %w", errorValue)
	}
	document := map[string]json.RawMessage{}
	if errorValue := json.Unmarshal(content, &document); errorValue != nil {
		return fmt.Errorf("parse the snapshot to restore its blanks: %w", errorValue)
	}
	var blanksAfter []officeBlank
	if raw, hasBlanks := document["blanks"]; hasBlanks {
		if errorValue := json.Unmarshal(raw, &blanksAfter); errorValue != nil {
			return fmt.Errorf("parse the rebuilt snapshot's blanks: %w", errorValue)
		}
	}
	merged := mergedOfficeBlanks(blanksBefore, blanksAfter)
	if len(merged) == len(blanksAfter) {
		return nil
	}
	document["blanks"] = json.RawMessage(MarshalBody(merged))
	return actor.WriteFile(ctx, snapshotPath, []byte(MarshalBody(document)))
}

func mergedOfficeBlanks(first []officeBlank, second []officeBlank) []officeBlank {
	merged := slices.Clone(first)
	for _, blank := range second {
		if !slices.ContainsFunc(first, func(known officeBlank) bool { return known.Field == blank.Field }) {
			merged = append(merged, blank)
		}
	}
	return merged
}

type officeDeck struct {
	toolCatalogBuilder *ToolCatalogBuilder
	request            ToolCatalogRequest
	actor              security.WorkspaceActor
	snapshot           officeSnapshot
	concretePath       string
}

func (deck *officeDeck) Manifest(ctx context.Context) (visualcheck.Manifest, error) {
	content, errorValue := deck.actor.ReadFile(ctx, deck.snapshot.VisualReview, officeVisualManifestReadLimit)
	if errorValue != nil {
		return visualcheck.Manifest{}, errorValue
	}
	return visualcheck.ParseManifest(content)
}

func (deck *officeDeck) Image(ctx context.Context, path string) ([]byte, error) {
	return deck.actor.ReadFile(ctx, path, officeVisualImageReadLimit)
}

func (deck *officeDeck) Rebuild(ctx context.Context, replacements map[int]string) (visualcheck.Manifest, error) {
	words, errorValue := officeRebuildWords(deck.snapshot)
	if errorValue != nil {
		return visualcheck.Manifest{}, errorValue
	}
	manifest, errorValue := deck.Manifest(ctx)
	if errorValue != nil {
		return visualcheck.Manifest{}, errorValue
	}
	original, errorValue := deck.replaceSections(ctx, manifest.Source, replacements)
	if errorValue != nil {
		return visualcheck.Manifest{}, errorValue
	}
	if detail := deck.toolCatalogBuilder.runOfficeCommand(ctx, deck.request, deck.actor, words); detail != "" {
		_ = deck.actor.WriteFile(ctx, manifest.Source, original)
		return visualcheck.Manifest{}, errors.New("the deck rebuild failed: " + detail)
	}
	return deck.rebuiltManifest(ctx)
}

func (deck *officeDeck) rebuiltManifest(ctx context.Context) (visualcheck.Manifest, error) {
	snapshotPath := deck.concretePath + officeContract.SourceSuffix
	content, errorValue := deck.actor.ReadFile(ctx, snapshotPath, officeSnapshotReadLimit)
	if errorValue != nil {
		return visualcheck.Manifest{}, errorValue
	}
	var rebuilt officeSnapshot
	if errorValue := json.Unmarshal(content, &rebuilt); errorValue != nil {
		return visualcheck.Manifest{}, errorValue
	}
	if rebuilt.VisualReview == "" {
		return visualcheck.Manifest{}, errors.New("the rebuilt deck names no visual review")
	}
	deck.snapshot = rebuilt
	return deck.Manifest(ctx)
}

func (deck *officeDeck) replaceSections(ctx context.Context, sourcePath string, replacements map[int]string) ([]byte, error) {
	source, errorValue := deck.actor.ReadFile(ctx, sourcePath, officeVisualSourceReadLimit)
	if errorValue != nil {
		return nil, errorValue
	}
	replaced, errorValue := withReplacedSections(string(source), replacements)
	if errorValue != nil {
		return nil, errorValue
	}
	return source, deck.actor.WriteFile(ctx, sourcePath, []byte(replaced))
}

func withReplacedSections(source string, replacements map[int]string) (string, error) {
	spans := slideSectionPattern.FindAllStringIndex(source, -1)
	for number := range replacements {
		if number < 1 || number > len(spans) {
			return "", fmt.Errorf("slide %d is not a section of the deck source", number)
		}
	}
	var builder strings.Builder
	cursor := 0
	for index, span := range spans {
		replacement, isReplaced := replacements[index+1]
		if !isReplaced {
			continue
		}
		builder.WriteString(source[cursor:span[0]])
		builder.WriteString(replacement)
		cursor = span[1]
	}
	builder.WriteString(source[cursor:])
	return builder.String(), nil
}

func officeRebuildWords(snapshot officeSnapshot) ([]string, error) {
	command := strings.Fields(snapshot.Command)
	if len(command) < 2 || command[0] != officeCommandPrefix || len(snapshot.Arguments) == 0 {
		return nil, errors.New("the snapshot records no office command to rebuild the deck with")
	}
	return append(slices.Clone(command[1:]), snapshot.Arguments...), nil
}

func visualReviewNotes(reviews []officeVisualReview) []string {
	notes := []string{}
	for _, review := range reviews {
		if review.Outcome == visualOutcomeLeftovers {
			notes = append(notes, review.File+": slides that still show a defect after the visual review, for the reply to say what remains: "+leftoverSlideNames(review.Leftovers))
		}
	}
	return notes
}

func leftoverSlideNames(leftovers []officeVisualLeftover) string {
	names := []string{}
	for _, leftover := range leftovers {
		name := fmt.Sprintf("slide %d", leftover.Number)
		if kinds := slices.Concat(leftover.Findings, leftover.Measured); len(kinds) > 0 {
			name += " (" + strings.Join(kinds, ", ") + ")"
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func appendedClaimRecheck(claimChecks []officeClaimCheck, review *officeVisualReview) []officeClaimCheck {
	if review.claimRecheck == nil {
		return claimChecks
	}
	return append(claimChecks, *review.claimRecheck)
}
