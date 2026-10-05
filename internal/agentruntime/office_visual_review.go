package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/officeclaimcheck"
	"github.com/yeomyeonggeori/blueclaw/internal/officevisualcheck"
	"github.com/yeomyeonggeori/bluecollar/model"

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

type officeVisualLeftover struct {
	Number   int      `json:"number"`
	Findings []string `json:"findings,omitempty"`
	Measured []string `json:"measured,omitempty"`
}

type officeVisualReview struct {
	File            string                    `json:"file"`
	RoundsUsed      int                       `json:"roundsUsed"`
	Fixed           []officevisualcheck.Fixed `json:"fixed,omitempty"`
	GivenUp         []int                     `json:"givenUp,omitempty"`
	Leftovers       []officeVisualLeftover    `json:"leftovers,omitempty"`
	TextChanged     []int                     `json:"textChanged,omitempty"`
	RecheckedClaims int                       `json:"recheckedClaims"`
	CostUSD         float64                   `json:"costUSD"`
	Outcome         string                    `json:"outcome"`
	Detail          string                    `json:"detail,omitempty"`

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
	report, errorValue := officevisualcheck.Run(ctx, toolCatalogBuilder.visualReviewDecisionModel, toolCatalogBuilder.visualReviewLanguageModel, deck)
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
	return nil
}

func (review *officeVisualReview) recordReport(report officevisualcheck.Report) {
	review.RoundsUsed = report.RoundsUsed
	review.Fixed = report.Fixed
	review.GivenUp = report.GivenUp
	review.TextChanged = report.TextChanged
	review.CostUSD = report.Usage.Decision.CostUSD + report.Usage.Language.CostUSD
	review.Leftovers = leftoversOf(report)
	review.Outcome = visualOutcomeOf(report)
}

func visualOutcomeOf(report officevisualcheck.Report) string {
	switch {
	case len(report.Leftovers) > 0:
		return visualOutcomeLeftovers
	case len(report.Fixed) > 0:
		return visualOutcomeFixed
	}
	return visualOutcomeClean
}

func leftoversOf(report officevisualcheck.Report) []officeVisualLeftover {
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

func newOrChangedClaims(before []officeclaimcheck.Claim, after []officeclaimcheck.Claim) []officeclaimcheck.Claim {
	known := map[officeclaimcheck.Claim]bool{}
	for _, claim := range before {
		known[officeclaimcheck.Claim{Path: claim.Path, Text: claim.Text}] = true
	}
	changed := []officeclaimcheck.Claim{}
	for _, claim := range after {
		if !known[officeclaimcheck.Claim{Path: claim.Path, Text: claim.Text}] {
			changed = append(changed, claim)
		}
	}
	return changed
}

type officeDeck struct {
	toolCatalogBuilder *ToolCatalogBuilder
	request            ToolCatalogRequest
	actor              security.WorkspaceActor
	snapshot           officeSnapshot
	concretePath       string
}

func (deck *officeDeck) Manifest(ctx context.Context) (officevisualcheck.Manifest, error) {
	content, errorValue := deck.actor.ReadFile(ctx, deck.snapshot.VisualReview, officeVisualManifestReadLimit)
	if errorValue != nil {
		return officevisualcheck.Manifest{}, errorValue
	}
	return officevisualcheck.ParseManifest(content)
}

func (deck *officeDeck) Image(ctx context.Context, path string) ([]byte, error) {
	return deck.actor.ReadFile(ctx, path, officeVisualImageReadLimit)
}

func (deck *officeDeck) Rebuild(ctx context.Context, replacements map[int]string) (officevisualcheck.Manifest, error) {
	words, errorValue := officeRebuildWords(deck.snapshot)
	if errorValue != nil {
		return officevisualcheck.Manifest{}, errorValue
	}
	manifest, errorValue := deck.Manifest(ctx)
	if errorValue != nil {
		return officevisualcheck.Manifest{}, errorValue
	}
	originals, errorValue := deck.writePages(ctx, manifest, replacements)
	if errorValue != nil {
		deck.restorePages(ctx, originals)
		return officevisualcheck.Manifest{}, errorValue
	}
	if detail := deck.toolCatalogBuilder.runOfficeCommand(ctx, deck.request, deck.actor, words); detail != "" {
		deck.restorePages(ctx, originals)
		return officevisualcheck.Manifest{}, errors.New("the deck rebuild failed: " + detail)
	}
	return deck.rebuiltManifest(ctx)
}

func (deck *officeDeck) rebuiltManifest(ctx context.Context) (officevisualcheck.Manifest, error) {
	snapshotPath := deck.concretePath + officeContract.SourceSuffix
	content, errorValue := deck.actor.ReadFile(ctx, snapshotPath, officeSnapshotReadLimit)
	if errorValue != nil {
		return officevisualcheck.Manifest{}, errorValue
	}
	var rebuilt officeSnapshot
	if errorValue := json.Unmarshal(content, &rebuilt); errorValue != nil {
		return officevisualcheck.Manifest{}, errorValue
	}
	if rebuilt.VisualReview == "" {
		return officevisualcheck.Manifest{}, errors.New("the rebuilt deck names no visual review")
	}
	deck.snapshot = rebuilt
	return deck.Manifest(ctx)
}

func (deck *officeDeck) writePages(ctx context.Context, manifest officevisualcheck.Manifest, replacements map[int]string) (map[string][]byte, error) {
	originals := map[string][]byte{}
	for number, section := range replacements {
		pagePath := manifest.PageSource(number)
		if pagePath == "" {
			return originals, fmt.Errorf("slide %d names no page file", number)
		}
		original, errorValue := deck.actor.ReadFile(ctx, pagePath, officeVisualSourceReadLimit)
		if errorValue != nil {
			return originals, errorValue
		}
		originals[pagePath] = original
		if errorValue := deck.actor.WriteFile(ctx, pagePath, []byte(section+"\n")); errorValue != nil {
			return originals, errorValue
		}
	}
	return originals, nil
}

func (deck *officeDeck) restorePages(ctx context.Context, originals map[string][]byte) {
	for pagePath, original := range originals {
		_ = deck.actor.WriteFile(ctx, pagePath, original)
	}
}

func officeRebuildWords(snapshot officeSnapshot) ([]string, error) {
	command := strings.Fields(snapshot.Command)
	if len(command) < 2 || command[0] != officeCommandPrefix || len(snapshot.Arguments) == 0 {
		return nil, errors.New("the snapshot records no office command to rebuild the deck with")
	}
	return append(slices.Clone(command[1:]), snapshot.Arguments...), nil
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
