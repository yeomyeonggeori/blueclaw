package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	Merge  []string `json:"merge"`
	Create []string `json:"create"`
	Deck   []string `json:"deck"`
}

type officeClaimCheck struct {
	File        string               `json:"file"`
	Asked       int                  `json:"asked"`
	Unsupported []claimcheck.Verdict `json:"unsupported,omitempty"`
	Blanked     []string             `json:"blanked,omitempty"`
	Outcome     string               `json:"outcome"`
	Detail      string               `json:"detail,omitempty"`
}

const (
	claimOutcomeSupported       = "supported"
	claimOutcomeBlanked         = "blanked"
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
	judgment, errorValue := claimcheck.Judge(ctx, toolCatalogBuilder.claimDecisionModel, sources, claims)
	if errorValue != nil {
		check.Outcome, check.Detail = claimOutcomeJudgeFailed, errorValue.Error()
		return check
	}
	check.Asked = askedCount(judgment)
	check.Unsupported = judgment.Unsupported()
	return toolCatalogBuilder.actOnUnsupportedClaims(ctx, request, actor, concretePath, snapshot, check, isEverySourceRead)
}

func (toolCatalogBuilder *ToolCatalogBuilder) actOnUnsupportedClaims(ctx context.Context, request ToolCatalogRequest, actor security.WorkspaceActor, concretePath string, snapshot officeSnapshot, check *officeClaimCheck, isEverySourceRead bool) *officeClaimCheck {
	if len(check.Unsupported) == 0 {
		check.Outcome = claimOutcomeSupported
		return check
	}
	if !isEverySourceRead {
		check.Outcome = claimOutcomeUnreadSources
		return check
	}
	paths := verdictPaths(check.Unsupported)
	words, hasCommand := officeRemakeWords(snapshot, concretePath, paths)
	if !hasCommand {
		check.Outcome = claimOutcomeNoRemakeCommand
		return check
	}
	if detail := toolCatalogBuilder.runOfficeCommand(ctx, request, actor, words); detail != "" {
		check.Outcome, check.Detail = claimOutcomeRemakeFailed, detail
		return check
	}
	check.Outcome, check.Blanked = claimOutcomeBlanked, verdictPlaces(check.Unsupported)
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

func officeRemakeWords(snapshot officeSnapshot, concretePath string, paths []string) ([]string, bool) {
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
		return errorValue.Error()
	}
	if commandResult.ExitCode != 0 {
		return firstNonEmptyString(strings.TrimSpace(commandResult.Stdout), strings.TrimSpace(commandResult.Stderr), "the office command failed")
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

func blankedClaimsNotes(checks []officeClaimCheck) []string {
	notes := []string{}
	for _, check := range checks {
		if check.Outcome == claimOutcomeBlanked {
			notes = append(notes, check.File+": left blank because nothing the person gave supports them, for the reply to offer to complete: "+strings.Join(blankedClaimsSaid(check.Unsupported), ", "))
		}
	}
	return notes
}

func blankedClaimsSaid(verdicts []claimcheck.Verdict) []string {
	said := []string{}
	for _, verdict := range verdicts {
		said = append(said, fmt.Sprintf("%s (it said %q)", firstNonEmptyString(verdict.At, verdict.Path), verdict.Text))
	}
	return said
}
