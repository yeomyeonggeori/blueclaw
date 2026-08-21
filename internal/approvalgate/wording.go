package approvalgate

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
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
	MessageID      string   `json:"messageID"`
	MessageIDs     []string `json:"messageIDs"`
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

func (gate *Gate) confirmationWording(ctx context.Context, approvalRequest mcpserver.ApprovalRequest) string {
	question, errorValue := gate.generateConfirmationWording(ctx, approvalRequest)
	if errorValue == nil {
		return question
	}
	return rawApprovalSummary(approvalRequest)
}

func (gate *Gate) generateConfirmationWording(ctx context.Context, approvalRequest mcpserver.ApprovalRequest) (string, error) {
	if gate.languageModel == nil {
		return "", errors.New("approval wording needs a language model provider and none is configured")
	}
	questionContext, errorValue := json.Marshal(approvalQuestionContext{
		ResponseLanguage: strings.TrimSpace(approvalRequest.ResponseLanguage),
		OriginalRequest:  strings.TrimSpace(approvalRequest.Prompt),
		ModelDraft:       strings.TrimSpace(approvalRequest.ModelDraft),
		Operation:        strings.TrimSpace(approvalRequest.ToolName),
		ActionDetails:    approvalQuestionActionDetails(approvalRequest.ToolInput),
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
				"Include consequential details when present so the user can approve a concrete action.",
				"When the action changes or removes something that already exists, say what it affects, and never describe a whole-item replacement as if it only touched a part of it.",
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

func rawApprovalSummary(approvalRequest mcpserver.ApprovalRequest) string {
	summary := strings.TrimSpace(approvalRequest.ToolName)
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

func approvalQuestionActionDetails(toolInput json.RawMessage) map[string]string {
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
	if len(details) == 0 {
		return nil
	}
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
