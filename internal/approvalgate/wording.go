package approvalgate

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type approvalQuestionContext struct {
	ResponseLanguage string            `json:"responseLanguage,omitempty"`
	OriginalRequest  string            `json:"originalRequest,omitempty"`
	ModelDraft       string            `json:"modelDraft,omitempty"`
	Operation        string            `json:"operation,omitempty"`
	ActionDetails    map[string]string `json:"actionDetails,omitempty"`
}

type approvalQuestionInput struct {
	PersonHint     string   `json:"personHint"`
	ChannelName    string   `json:"channelName"`
	To             []string `json:"to"`
	People         []string `json:"people"`
	TargetType     string   `json:"targetType"`
	Message        string   `json:"message"`
	Subject        string   `json:"subject"`
	Body           string   `json:"body"`
	Title          string   `json:"title"`
	Summary        string   `json:"summary"`
	Reason         string   `json:"reason"`
	ApprovalReason string   `json:"approvalReason"`
	Slug           string   `json:"slug"`
	SiteID         string   `json:"siteID"`
	EventHint      string   `json:"eventHint"`
	Path           string   `json:"path"`
	DevicePath     string   `json:"devicePath"`
	TargetPath     string   `json:"targetPath"`
}

func (gate *Gate) confirmationWording(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, target ApprovalTarget) string {
	question, errorValue := gate.generateConfirmationWording(ctx, approvalRequest, target)
	if errorValue == nil {
		return question
	}
	return rawApprovalSummary(approvalRequest, target)
}

func (gate *Gate) generateConfirmationWording(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, target ApprovalTarget) (string, error) {
	if gate.languageModel == nil {
		return "", errors.New("approval wording needs a language model provider and none is configured")
	}
	questionContext, errorValue := json.Marshal(approvalQuestionContext{
		ResponseLanguage: strings.TrimSpace(approvalRequest.ResponseLanguage),
		OriginalRequest:  strings.TrimSpace(approvalRequest.Prompt),
		ModelDraft:       strings.TrimSpace(approvalRequest.ModelDraft),
		Operation:        strings.TrimSpace(approvalRequest.ToolName),
		ActionDetails:    approvalQuestionActionDetails(approvalRequest.ToolInput, target),
	})
	if errorValue != nil {
		return "", errorValue
	}
	structuredResponse, errorValue := gate.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: strings.Join([]string{
				"Write exactly one concise user-facing approval question.",
				"The question asks whether to perform the pending action.",
				"Use the original request and action details to phrase the target, content, file, event, or site naturally.",
				"When the action details name a resolved target, name that target and never repeat a search phrase the caller typed.",
				"Include consequential details when present so the user can approve a concrete action.",
				"Do not mention internal tool names, operation identifiers, JSON, schemas, approval gates, runtime, or implementation details.",
				"Do not answer the question, report status, or explain the policy.",
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

func rawApprovalSummary(approvalRequest mcpserver.ApprovalRequest, target ApprovalTarget) string {
	summary := strings.TrimSpace(approvalRequest.ToolName)
	if target.isResolved() {
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
	setApprovalQuestionDetail(details, "content", firstNonEmpty(document.Message, document.Subject, document.Body, document.Title, document.Summary, document.ApprovalReason, document.Reason))
	setApprovalQuestionDetail(details, "message", document.Message)
	setApprovalQuestionDetail(details, "subject", document.Subject)
	setApprovalQuestionDetail(details, "title", document.Title)
	setApprovalQuestionDetail(details, "summary", document.Summary)
	setApprovalQuestionDetail(details, "reason", firstNonEmpty(document.Reason, document.ApprovalReason))
	setApprovalQuestionDetail(details, "slug", document.Slug)
	setApprovalQuestionDetail(details, "siteID", document.SiteID)
	setApprovalQuestionDetail(details, "eventHint", document.EventHint)
	filePath := firstNonEmpty(document.Path, document.DevicePath, document.TargetPath)
	setApprovalQuestionDetail(details, "path", filePath)
	if strings.TrimSpace(filePath) != "" {
		setApprovalQuestionDetail(details, "fileName", filepath.Base(filePath))
	}
	details = detailsNamingTheResolvedTarget(details, target)
	if len(details) == 0 {
		return nil
	}
	return details
}

func detailsNamingTheResolvedTarget(details map[string]string, target ApprovalTarget) map[string]string {
	if !target.isResolved() {
		return details
	}
	delete(details, strings.TrimSpace(target.InputField))
	setApprovalQuestionDetail(details, "resolvedTarget", target.Title)
	setApprovalQuestionDetail(details, "resolvedTargetStartsAt", target.StartsAt)
	return details
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
