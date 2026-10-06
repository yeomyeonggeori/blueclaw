package approvalgate

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type approvalQuestionContext struct {
	ResponseLanguage string              `json:"responseLanguage,omitempty"`
	OriginalRequest  string              `json:"originalRequest,omitempty"`
	ModelDraft       string              `json:"modelDraft,omitempty"`
	Operation        string              `json:"operation,omitempty"`
	ApprovalScope    string              `json:"approvalScope,omitempty"`
	ActionDetails    map[string]string   `json:"actionDetails,omitempty"`
	Choices          []holdrecord.Choice `json:"choices,omitempty"`
}

type approvalQuestionInput struct {
	PersonHint     string   `json:"personHint"`
	ChannelName    string   `json:"channelName"`
	To             []string `json:"to"`
	People         []string `json:"people"`
	TargetType     string   `json:"targetType"`
	MessageID      string   `json:"messageID"`
	MessageIDs     []string `json:"messageIDs"`
	Message        string   `json:"message"`
	OldText        string   `json:"oldText"`
	NewText        string   `json:"newText"`
	Subject        string   `json:"subject"`
	Body           string   `json:"body"`
	Title          string   `json:"title"`
	Summary        string   `json:"summary"`
	Reason         string   `json:"reason"`
	ApprovalReason string   `json:"approvalReason"`
	EventHint      string   `json:"eventHint"`
	Path           string   `json:"path"`
	DevicePath     string   `json:"devicePath"`
	TargetPath     string   `json:"targetPath"`
}

const TaskEventApprovalWordingFailed = "approval.wording_failed"

func (gate *Gate) confirmationWording(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, resolution ApprovalTargetResolution) string {
	question, errorValue := gate.generateConfirmationWording(ctx, approvalRequest, resolution)
	if errorValue == nil {
		return question
	}
	gate.recordWordingFailure(approvalRequest, errorValue)
	return rawApprovalSummary(approvalRequest, resolution)
}

func (gate *Gate) recordWordingFailure(approvalRequest mcpserver.ApprovalRequest, errorValue error) {
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	toolName := strings.TrimSpace(approvalRequest.ToolName)
	slog.Warn("approvalgate.wording_failed", "taskRunID", taskRunID, "toolName", toolName, "error", errorValue.Error())
	if taskRunID == "" {
		return
	}
	gate.taskRunService.AppendTaskEvent(taskRunID, TaskEventApprovalWordingFailed, marshalEventBody(map[string]string{
		"toolName": toolName,
		"error":    errorValue.Error(),
	}))
}

func (gate *Gate) generateConfirmationWording(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, resolution ApprovalTargetResolution) (string, error) {
	if gate.languageModel == nil {
		return "", errors.New("approval wording needs a language model provider and none is configured")
	}
	questionContext, errorValue := json.Marshal(approvalQuestionContext{
		ResponseLanguage: strings.TrimSpace(approvalRequest.ResponseLanguage),
		OriginalRequest:  strings.TrimSpace(approvalRequest.Prompt),
		ModelDraft:       strings.TrimSpace(approvalRequest.ModelDraft),
		Operation:        strings.TrimSpace(approvalRequest.ToolName),
		ApprovalScope:    strings.TrimSpace(approvalRequest.ApprovalScope),
		ActionDetails:    approvalQuestionActionDetails(approvalRequest.ToolInput, resolution.Target),
		Choices:          resolution.Choices,
	})
	if errorValue != nil {
		return "", errorValue
	}
	structuredResponse, errorValue := gate.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: strings.Join([]string{
				"Write exactly one user-facing approval question.",
				"The question asks whether to perform the pending action.",
				"The question is the user's only view of the action, so it must show exactly what will happen, never a category of thing that will happen: 'post this?' is worthless, 'post \"…\"?' is the question.",
				"When the action sends or posts content, quote the content verbatim in the question (a blockquote under one asking sentence works). Never paraphrase, summarize, or shorten it — what the user approves is exactly what will appear.",
				"When the action replaces a span of text, show the span being replaced and its replacement, both verbatim.",
				"When the action removes something, quote what will be removed — its text or the given preview — so the user can tell it from everything it is not.",
				"Use the original request and action details to phrase the target, content, file, or event naturally.",
				"When the action details name a resolved target or carry a target preview, show that and never repeat a search phrase the caller typed.",
				"When the action changes or removes something that already exists, say what it affects, and never describe a whole-item replacement as if it only touched a part of it.",
				"Keep the asking sentence short; the quoted content is as long as it is.",
				"Do not mention internal tool names, operation identifiers, JSON, schemas, approval gates, runtime, or implementation details.",
				"Do not answer the question, report status, or explain the policy.",
				"The question covers this one action and nothing after it. The original request is there to name what the action touches, never to describe the work it is a step toward.",
				"Never promise a later step this action does not perform. Approving it must not read as approving anything that has to happen afterwards.",
				"When an approvalScope is given, approving this action also approves every later action of that scope for the rest of this task, and the question must say so in plain words, naming what the scope covers. Do not mention the scope when none is given.",
				"When the target preview lists what the action will cause, state each of those consequences plainly; a requester approving it must know them.",
				"When choices are given, the question offers exactly those choices, in the order given, followed by cancelling, as a short numbered list the requester can answer by number. A choice with startsAt runs the action at that moment, written as a local date and time; a choice without startsAt runs it now. Offer no choice that is not given.",
			}, "\n")},
			{Role: "system", Content: responseLanguageInstruction(approvalRequest.ResponseLanguage)},
			{Role: "user", Content: string(questionContext)},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               "blueclaw_approval_question",
			Document:           `{"type":"object","properties":{"question":{"type":"string"}},"required":["question"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return "", errorValue
	}
	answer := struct {
		Question string `json:"question"`
	}{}
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &answer); errorValue != nil {
		return "", errorValue
	}
	question := strings.TrimSpace(answer.Question)
	if question == "" {
		return "", errors.New("the model returned an empty approval question")
	}
	return question, nil
}

func rawApprovalSummary(approvalRequest mcpserver.ApprovalRequest, resolution ApprovalTargetResolution) string {
	target := resolution.Target
	summary := strings.TrimSpace(approvalRequest.ToolName)
	if target.IsResolved() {
		return strings.TrimSpace(summary + " " + firstNonEmpty(target.Title, target.ID))
	}
	if toolInput := strings.TrimSpace(string(approvalRequest.ToolInput)); toolInput != "" && toolInput != "{}" {
		summary += " " + toolInput
	}
	return summary
}

func responseLanguageInstruction(responseLanguage string) string {
	if toolcontract.ResolveResponseLanguage(responseLanguage) == toolcontract.ResponseLanguageEnglish {
		return "Write in English."
	}
	return "Write in Korean."
}

func approvalQuestionActionDetails(toolInput json.RawMessage, target ApprovalTarget) map[string]string {
	if len(toolInput) == 0 {
		return nil
	}
	var document approvalQuestionInput
	if json.Unmarshal(toolInput, &document) != nil {
		return nil
	}
	details := map[string]string{}
	setApprovalQuestionDetail(details, "target", firstNonEmpty(document.PersonHint, document.ChannelName, strings.Join(document.To, ", "), strings.Join(document.People, ", ")))
	setApprovalQuestionDetail(details, "deliveryTargetType", document.TargetType)
	setApprovalQuestionDetail(details, "targetMessageIDs", firstNonEmpty(document.MessageID, strings.Join(document.MessageIDs, ", ")))
	setApprovalQuestionDetail(details, "targetMessageCount", approvalQuestionMessageCount(document))
	setApprovalQuestionDetail(details, "content", firstNonEmpty(document.Message, document.Subject, document.Body, document.Title, document.Summary))
	setApprovalQuestionDetail(details, "message", document.Message)
	setApprovalQuestionDetail(details, "replacedText", document.OldText)
	setApprovalQuestionDetail(details, "replacementText", document.NewText)
	setApprovalQuestionDetail(details, "subject", document.Subject)
	setApprovalQuestionDetail(details, "title", document.Title)
	setApprovalQuestionDetail(details, "summary", document.Summary)
	setApprovalQuestionDetail(details, "reason", firstNonEmpty(document.Reason, document.ApprovalReason))
	setApprovalQuestionDetail(details, "eventHint", document.EventHint)
	filePath := firstNonEmpty(document.Path, document.DevicePath, document.TargetPath)
	setApprovalQuestionDetail(details, "path", filePath)
	if strings.TrimSpace(filePath) != "" {
		setApprovalQuestionDetail(details, "fileName", filepath.Base(filePath))
	}
	details = detailsNamingTheResolvedTarget(details, target)
	setApprovalQuestionDetail(details, "targetPreview", target.Preview)
	if len(details) == 0 {
		return nil
	}
	return details
}

func detailsNamingTheResolvedTarget(details map[string]string, target ApprovalTarget) map[string]string {
	if !target.IsResolved() {
		return details
	}
	delete(details, strings.TrimSpace(target.InputField))
	setApprovalQuestionDetail(details, "resolvedTarget", target.Title)
	setApprovalQuestionDetail(details, "resolvedTargetStartsAt", target.StartsAt)
	return details
}

func approvalQuestionMessageCount(document approvalQuestionInput) string {
	if len(document.MessageIDs) == 0 {
		return ""
	}
	return strconv.Itoa(len(document.MessageIDs))
}

func setApprovalQuestionDetail(details map[string]string, key string, value string) {
	if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
		details[key] = trimmedValue
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
