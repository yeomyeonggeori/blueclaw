package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

type capabilityApprovalTargetResolver struct {
	capabilityClient capability.Client
}

type capabilityTargetResolveResponse struct {
	ToolName     string          `json:"toolName"`
	Content      string          `json:"content"`
	IsError      bool            `json:"isError"`
	Status       string          `json:"status"`
	Message      string          `json:"message"`
	ErrorCode    string          `json:"errorCode"`
	FailureStage string          `json:"failureStage"`
	Retryable    bool            `json:"retryable"`
	SafeRetry    bool            `json:"safeRetry"`
	Result       json.RawMessage `json:"result"`
}

func NewCapabilityApprovalTargetResolver(capabilityClient capability.Client) approvalgate.ApprovalTargetResolver {
	return capabilityApprovalTargetResolver{capabilityClient: capabilityClient}
}

func (resolver capabilityApprovalTargetResolver) ResolveApprovalTarget(ctx context.Context, request approvalgate.ApprovalTargetRequest) (approvalgate.ApprovalTargetResolution, error) {
	operation := strings.TrimSpace(request.ToolName)
	if operation == "" {
		return approvalgate.ApprovalTargetResolution{}, nil
	}
	var response capabilityTargetResolveResponse
	errorValue := resolver.capabilityClient.PostJSON(ctx, "/v1/tools/"+url.PathEscape(operation)+"/target.resolve", capabilityTargetResolveRequest(operation, request), &response)
	if errorValue != nil {
		return approvalgate.ApprovalTargetResolution{}, errorValue
	}
	if strings.TrimSpace(response.ToolName) != operation {
		return approvalgate.ApprovalTargetResolution{}, errors.New("capability target resolution answered for " + strings.TrimSpace(response.ToolName) + " instead of " + operation)
	}
	return response.approvalTargetResolution(), nil
}

func capabilityTargetResolveRequest(operation string, request approvalgate.ApprovalTargetRequest) map[string]any {
	return map[string]any{
		"toolName": operation,
		"input":    request.ToolInput,
		"context": map[string]any{
			"requesterPersonID": request.RequesterPersonID,
			"requesterEmail":    request.RequesterEmail,
		},
	}
}

func (response capabilityTargetResolveResponse) approvalTargetResolution() approvalgate.ApprovalTargetResolution {
	if response.isFailed() {
		return approvalgate.ApprovalTargetResolution{Failure: capabilityToolResult(
			response.failureContent(),
			response.Result,
			nil,
			true,
			response.Message,
			response.ErrorCode,
			response.FailureStage,
			response.Retryable,
			response.SafeRetry,
		)}
	}
	target := approvalgate.ApprovalTarget{}
	if json.Unmarshal(response.Result, &target) != nil {
		return approvalgate.ApprovalTargetResolution{}
	}
	return approvalgate.ApprovalTargetResolution{Target: target}
}

func (response capabilityTargetResolveResponse) isFailed() bool {
	return response.IsError || response.Status == "error" || response.Status == "denied"
}

func (response capabilityTargetResolveResponse) failureContent() string {
	if content := strings.TrimSpace(response.Content); content != "" {
		return content
	}
	return string(response.Result)
}
