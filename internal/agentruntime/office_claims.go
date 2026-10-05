package agentruntime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/claimcheck"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const (
	officeSnapshotReadLimit    = 1 << 20
	officeBlankCommandTimeout  = 180
	officeBlankCommandMaxBytes = 65536
	officeEntryRelativePath    = "office/scripts/office"
	maximumWithdrawalPasses    = 2
)

type officeSnapshot struct {
	Schema      string             `json:"schema"`
	Declaration string             `json:"declaration"`
	Deck        string             `json:"deck"`
	Known       json.RawMessage    `json:"known"`
	Claims      []claimcheck.Claim `json:"claims"`
	Command     string             `json:"command"`
	Arguments   []string           `json:"arguments"`
	Blanks      []officeBlank      `json:"blanks"`

	VisualReview string `json:"visualReview"`
}

type officeBlank struct {
	Field string `json:"field"`
	Label string `json:"label"`
}

type officeBlankCommands struct {
	ReplaceFlag string   `json:"replaceFlag"`
	Merge       []string `json:"merge"`
	Create      []string `json:"create"`
	Deck        []string `json:"deck"`
}

type officeClaimCheck struct {
	File    string               `json:"file"`
	Asked   int                  `json:"asked"`
	Flagged []claimcheck.Verdict `json:"flagged,omitempty"`
	Hollow  []claimcheck.Verdict `json:"hollow,omitempty"`
	Blanked []string             `json:"blanked,omitempty"`
	Outcome string               `json:"outcome"`
	Detail  string               `json:"detail,omitempty"`
}

const (
	claimOutcomeSupported       = "supported"
	claimOutcomeBlanked         = "blanked"
	claimOutcomeRewritten       = "rewritten"
	claimOutcomeUnreadSources   = "not_enforced_unread_attachment"
	claimOutcomeJudgeFailed     = "judge_failed"
	claimOutcomeRemakeFailed    = "remake_failed"
	claimOutcomeNoRemakeCommand = "no_remake_command"
)

var officeBlankCommand = parsedOfficeBlankCommands(officeHostContractDocument)

func parsedOfficeBlankCommands(document []byte) officeBlankCommands {
	var contract struct {
		BlankCommand officeBlankCommands `json:"blankCommand"`
	}
	if errorValue := json.Unmarshal(document, &contract); errorValue != nil {
		panic("office_host_contract.json blankCommand does not parse: " + errorValue.Error())
	}
	return contract.BlankCommand
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseClaimDecisionModel(decisionModel model.DecisionModel) {
	toolCatalogBuilder.claimDecisionModel = decisionModel
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseClaimRewrite(languageModel model.LanguageModelProvider) {
	toolCatalogBuilder.claimRewriteModel = languageModel
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseClaimRecompute(languageModel model.LanguageModelProvider) {
	toolCatalogBuilder.claimRecomputeModel = languageModel
}

func (toolCatalogBuilder *ToolCatalogBuilder) checkOfficeClaims(ctx context.Context, request ToolCatalogRequest, concretePath string) *officeClaimCheck {
	if toolCatalogBuilder.claimDecisionModel == nil {
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
	if !isRead || len(snapshot.Claims) == 0 {
		return nil
	}
	return toolCatalogBuilder.judgeAndBlank(ctx, request, actor, concretePath, snapshot, snapshot.Claims)
}

func (toolCatalogBuilder *ToolCatalogBuilder) judgeAndBlank(ctx context.Context, request ToolCatalogRequest, actor security.WorkspaceActor, concretePath string, snapshot officeSnapshot, claims []claimcheck.Claim) *officeClaimCheck {
	check := &officeClaimCheck{File: filepath.Base(concretePath)}
	sources, isEverySourceRead := toolCatalogBuilder.claimSources(ctx, request, snapshot)
	judgment, errorValue := claimcheck.Judge(ctx, toolCatalogBuilder.claimDecisionModel, sources, claimsMarkedFree(claims))
	if errorValue != nil {
		check.Outcome, check.Detail = claimOutcomeJudgeFailed, errorValue.Error()
		return check
	}
	judgment, check.Detail = toolCatalogBuilder.withRecomputedDerivations(ctx, sources, judgment)
	check.Asked = askedCount(judgment)
	check.Hollow = judgment.Treated(claimcheck.TreatmentRewrite)
	treated := toolCatalogBuilder.treatedClaims(ctx, sources, judgment, check)
	check.Flagged = treated.Blank
	check = toolCatalogBuilder.actOnFlaggedClaims(ctx, request, actor, concretePath, snapshot, check, treated, isEverySourceRead)
	return toolCatalogBuilder.judgeWhatTheBlanksLeft(ctx, request, actor, concretePath, sources, check, slices.Concat(treated.Blank, treated.Removed))
}

func (toolCatalogBuilder *ToolCatalogBuilder) judgeWhatTheBlanksLeft(ctx context.Context, request ToolCatalogRequest, actor security.WorkspaceActor, concretePath string, sources claimcheck.Sources, check *officeClaimCheck, newlyRemoved []claimcheck.Verdict) *officeClaimCheck {
	removed := verdictClaims(newlyRemoved)
	for pass := 0; pass < maximumWithdrawalPasses && check.Outcome == claimOutcomeBlanked && len(newlyRemoved) > 0; pass++ {
		snapshot, isRead := readOfficeSnapshot(ctx, actor, concretePath)
		neighbors := claimsMarkedFree(claimsSharingAParent(newlyRemoved, removed, snapshot.Claims))
		if !isRead || len(neighbors) == 0 {
			return check
		}
		judgment, errorValue := claimcheck.Judge(ctx, toolCatalogBuilder.claimDecisionModel, claimcheck.Sources{Request: sources.Request, Attachments: sources.Attachments, RuntimeFacts: sources.RuntimeFacts, Removed: removed}, neighbors)
		if errorValue != nil {
			check.Detail = "the statements left beside the blanks were not re-judged: " + errorValue.Error()
			return check
		}
		check.Asked += askedCount(judgment)
		newlyRemoved = judgment.Treated(claimcheck.TreatmentBlank)
		if len(newlyRemoved) == 0 {
			return check
		}
		words, hasCommand := officeRemakeWords(snapshot, concretePath, verdictPaths(newlyRemoved), nil)
		if detail := toolCatalogBuilder.runOfficeCommand(ctx, request, actor, words); !hasCommand || detail != "" {
			check.Detail = "the statements left beside the blanks could not be blanked: " + detail
			return check
		}
		removed = append(removed, verdictClaims(newlyRemoved)...)
		check.Flagged = append(check.Flagged, newlyRemoved...)
		check.Blanked = append(check.Blanked, verdictPlaces(newlyRemoved)...)
	}
	return check
}

func verdictClaims(verdicts []claimcheck.Verdict) []claimcheck.Claim {
	claims := []claimcheck.Claim{}
	for _, verdict := range verdicts {
		claims = append(claims, verdict.Claim)
	}
	return claims
}

func claimsSharingAParent(newlyRemoved []claimcheck.Verdict, removedSoFar []claimcheck.Claim, remaining []claimcheck.Claim) []claimcheck.Claim {
	parents := map[string]bool{}
	for _, verdict := range newlyRemoved {
		parents[parentPath(verdict.Path)] = true
	}
	neighbors := []claimcheck.Claim{}
	for _, claim := range remaining {
		if parents[parentPath(claim.Path)] && !isAmongClaims(claim, removedSoFar) {
			neighbors = append(neighbors, claim)
		}
	}
	return neighbors
}

func isAmongClaims(claim claimcheck.Claim, claims []claimcheck.Claim) bool {
	return slices.ContainsFunc(claims, func(other claimcheck.Claim) bool {
		return other.Path == claim.Path && other.At == claim.At && other.Text == claim.Text
	})
}

func parentPath(path string) string {
	withoutPiece, _, _ := strings.Cut(path, "#")
	if index := strings.LastIndex(withoutPiece, "["); index >= 0 {
		withoutPiece = withoutPiece[:index]
	}
	if index := strings.LastIndex(withoutPiece, "."); index >= 0 {
		return withoutPiece[:index]
	}
	return ""
}

func (toolCatalogBuilder *ToolCatalogBuilder) treatedClaims(ctx context.Context, sources claimcheck.Sources, judgment claimcheck.Judgment, check *officeClaimCheck) claimcheck.Outcome {
	if toolCatalogBuilder.claimRewriteModel == nil {
		return claimcheck.Outcome{Blank: judgment.Treated(claimcheck.TreatmentBlank)}
	}
	treated, errorValue := claimcheck.Treat(ctx, claimcheck.CompactProfile, toolCatalogBuilder.claimDecisionModel, toolCatalogBuilder.claimRewriteModel, sources, judgment)
	if errorValue != nil {
		check.Detail = firstNonEmptyString(check.Detail, "rewrite_failed: "+errorValue.Error())
		return claimcheck.Outcome{Blank: judgment.Treated(claimcheck.TreatmentBlank)}
	}
	return treated
}

func claimsMarkedFree(claims []claimcheck.Claim) []claimcheck.Claim {
	marked := make([]claimcheck.Claim, 0, len(claims))
	for _, claim := range claims {
		claim.IsFree = isSentencePath(claim.Path)
		marked = append(marked, claim)
	}
	return marked
}

func isSentencePath(path string) bool {
	_, sentence, isSentence := strings.Cut(path, "#")
	if !isSentence || sentence == "" {
		return false
	}
	for _, character := range sentence {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (toolCatalogBuilder *ToolCatalogBuilder) withRecomputedDerivations(ctx context.Context, sources claimcheck.Sources, judgment claimcheck.Judgment) (claimcheck.Judgment, string) {
	if toolCatalogBuilder.claimRecomputeModel == nil {
		return judgment, ""
	}
	rechecked, errorValue := claimcheck.Recompute(ctx, claimcheck.CompactProfile, toolCatalogBuilder.claimRecomputeModel, sources, judgment)
	if errorValue != nil {
		return judgment, "recompute_failed: " + errorValue.Error()
	}
	return rechecked, ""
}

func (toolCatalogBuilder *ToolCatalogBuilder) actOnFlaggedClaims(ctx context.Context, request ToolCatalogRequest, actor security.WorkspaceActor, concretePath string, snapshot officeSnapshot, check *officeClaimCheck, treated claimcheck.Outcome, isEverySourceRead bool) *officeClaimCheck {
	if len(treated.Blank) == 0 && len(treated.Removed) == 0 && len(treated.Replaced) == 0 {
		check.Outcome = claimOutcomeSupported
		return check
	}
	if !isEverySourceRead {
		check.Outcome = claimOutcomeUnreadSources
		return check
	}
	paths := append(verdictPaths(treated.Blank), verdictPaths(treated.Removed)...)
	words, hasCommand := officeRemakeWords(snapshot, concretePath, paths, treated.Replaced)
	if !hasCommand {
		check.Outcome = claimOutcomeNoRemakeCommand
		return check
	}
	if detail := toolCatalogBuilder.runOfficeCommand(ctx, request, actor, words); detail != "" {
		check.Outcome, check.Detail = claimOutcomeRemakeFailed, detail
		return check
	}
	check.Outcome, check.Blanked = claimOutcomeBlanked, verdictPlaces(treated.Blank)
	if len(paths) == 0 {
		check.Outcome = claimOutcomeRewritten
	}
	return check
}

func (toolCatalogBuilder *ToolCatalogBuilder) isWrittenInThisTask(ctx context.Context, actor security.WorkspaceActor, concretePath string) bool {
	taskStartedAt, isTaskKnown := toolCatalogBuilder.taskRunCreatedAt(toolcontract.TaskRunIDFromContext(ctx))
	if !isTaskKnown {
		return false
	}
	information, errorValue := actor.Stat(ctx, concretePath)
	return errorValue == nil && information.ModifiedAtUnix >= taskStartedAt.Unix()
}

func readOfficeSnapshot(ctx context.Context, actor security.WorkspaceActor, concretePath string) (officeSnapshot, bool) {
	content, isRead := readOfficeSnapshotDocument(ctx, actor, concretePath)
	if !isRead {
		return officeSnapshot{}, false
	}
	var snapshot officeSnapshot
	return snapshot, json.Unmarshal(content, &snapshot) == nil
}

func readOfficeSnapshotDocument(ctx context.Context, actor security.WorkspaceActor, concretePath string) ([]byte, bool) {
	content, errorValue := actor.ReadFile(ctx, concretePath+officeContract.SourceSuffix, officeSnapshotReadLimit)
	return content, errorValue == nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) claimSources(ctx context.Context, request ToolCatalogRequest, snapshot officeSnapshot) (claimcheck.Sources, bool) {
	runtimeContext := toolCatalogBuilder.officeRuntimeContextFor(request, toolcontract.TaskRunIDFromContext(ctx))
	facts := map[string]any{"today": runtimeContext.Today, "requester": runtimeContext.Requester}
	if len(snapshot.Known) > 0 && string(snapshot.Known) != "null" {
		facts["known"] = snapshot.Known
	}
	sources := claimcheck.Sources{Request: requestWordings(request), RuntimeFacts: json.RawMessage(MarshalBody(facts))}
	isEveryAttachmentRead := true
	for _, material := range request.VisibleContext.CurrentMaterials {
		if strings.TrimSpace(material.MarkdownPreview) == "" {
			isEveryAttachmentRead = false
			continue
		}
		sources.Attachments = append(sources.Attachments, claimcheck.Attachment{Name: material.Filename, Text: material.MarkdownPreview})
	}
	return sources, isEveryAttachmentRead
}

func requestWordings(request ToolCatalogRequest) []string {
	wordings := []string{}
	for _, message := range request.VisibleContext.Messages {
		if text := strings.TrimSpace(message.Text); text != "" {
			wordings = append(wordings, text)
		}
	}
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		wordings = append(wordings, prompt)
	}
	return wordings
}

func officeRemakeWords(snapshot officeSnapshot, concretePath string, paths []string, replacements []claimcheck.Claim) ([]string, bool) {
	template, values := []string(nil), map[string]string{"<file>": concretePath, "<sourceSuffix>": officeContract.SourceSuffix}
	switch {
	case snapshot.Schema != "":
		template, values["<schema>"] = officeBlankCommand.Merge, snapshot.Schema
	case snapshot.Declaration != "":
		template = officeBlankCommand.Create
	case snapshot.Deck != "":
		template, values["<deck>"] = officeBlankCommand.Deck, snapshot.Deck
	}
	if len(template) < 2 || template[len(template)-1] != "<path>" {
		return nil, false
	}
	words := []string{}
	for _, word := range template[:len(template)-2] {
		words = append(words, substitutedWord(word, values))
	}
	for _, path := range paths {
		words = append(words, template[len(template)-2], path)
	}
	for _, replacement := range replacements {
		words = append(words, officeBlankCommand.ReplaceFlag, replacement.Path+"="+replacement.Text)
	}
	return words, true
}

func substitutedWord(word string, values map[string]string) string {
	for placeholder, value := range values {
		word = strings.ReplaceAll(word, placeholder, value)
	}
	return word
}

func (toolCatalogBuilder *ToolCatalogBuilder) runOfficeCommand(ctx context.Context, request ToolCatalogRequest, actor security.WorkspaceActor, words []string) string {
	requesterHomePath := toolCatalogBuilder.requesterHomePath(request)
	quoted := []string{shellSingleQuoted(filepath.Join(toolCatalogBuilder.bundledSkillRootPath(), officeEntryRelativePath))}
	for _, word := range words {
		quoted = append(quoted, shellSingleQuoted(word))
	}
	commandResult, errorValue := actor.Run(ctx, security.CommandRequest{
		Command:              strings.Join(quoted, " "),
		WorkingDirectoryPath: requesterHomePath,
		EnvironmentVariables: toolCatalogBuilder.terminalEnvironmentVariables(nil, requesterHomePath, toolcontract.TaskRunIDFromContext(ctx)),
		TimeoutSecond:        officeBlankCommandTimeout,
		OutputMaximumBytes:   officeBlankCommandMaxBytes,
		ExecutionIdentity:    toolCatalogBuilder.executionIdentityForRequester(request),
	})
	if errorValue != nil {
		return firstNonEmptyString(strings.TrimSpace(commandResult.Stderr), strings.TrimSpace(commandResult.Stdout), errorValue.Error())
	}
	if commandResult.ExitCode != 0 {
		return firstNonEmptyString(strings.TrimSpace(commandResult.Stderr), strings.TrimSpace(commandResult.Stdout), "the office command failed")
	}
	return ""
}

func askedCount(judgment claimcheck.Judgment) int {
	asked := 0
	for _, verdict := range judgment.Verdicts {
		if !verdict.IsCopied {
			asked++
		}
	}
	return asked
}

func verdictPaths(verdicts []claimcheck.Verdict) []string {
	paths := []string{}
	for _, verdict := range verdicts {
		paths = append(paths, verdict.Path)
	}
	return paths
}

func verdictPlaces(verdicts []claimcheck.Verdict) []string {
	places := []string{}
	for _, verdict := range verdicts {
		places = append(places, firstNonEmptyString(verdict.At, verdict.Path))
	}
	return places
}
