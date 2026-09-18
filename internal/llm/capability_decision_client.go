package llm

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

// CapabilityDecisionClient asks capabilityd's decision route. A decision model
// answers closed questions and writes nothing, so there is no schema, no
// messages, and no fallback: a call either answers or fails.
type CapabilityDecisionClient struct {
	CapabilityLLMClient
}

type capabilityDecisionRequestDocument struct {
	Model     string                            `json:"model,omitempty"`
	State     any                               `json:"state"`
	Questions map[string]model.DecisionQuestion `json:"questions"`
	SessionID string                            `json:"sessionID,omitempty"`
}

type capabilityDecisionResponseDocument struct {
	Answers          map[string]model.DecisionAnswer `json:"answers"`
	Usage            capabilityDecisionUsageDocument `json:"usage"`
	ModelName        string                          `json:"modelName"`
	ProviderName     string                          `json:"providerName"`
	UpstreamProvider string                          `json:"upstreamProvider"`
	LatencyMS        int64                           `json:"latencyMs"`
}

func (decisionClient CapabilityDecisionClient) Decide(responseContext context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if decisionClient.CapabilityClient.HTTPClient == nil {
		return model.DecisionResponse{}, errors.New("capability decision http client is not configured")
	}
	if len(request.Questions) == 0 {
		return model.DecisionResponse{}, errors.New("a decision call carries no question")
	}
	requestDocument := capabilityDecisionRequestDocument{
		Model:     firstNonEmptyDecisionValue(request.Model, decisionClient.ModelName),
		State:     request.State,
		Questions: request.Questions,
		SessionID: request.SessionID,
	}
	startedAt := time.Now()
	var responseDocument capabilityDecisionResponseDocument
	if errorValue := decisionClient.postJSONWithRetry(responseContext, "/v1/llm/decide", requestDocument, &responseDocument); errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	if len(responseDocument.Answers) == 0 {
		return model.DecisionResponse{}, errors.New("the decision route answered no question")
	}
	return model.DecisionResponse{
		Answers:          typedDecisionAnswers(responseDocument.Answers, request.Questions),
		Usage:            decisionUsage(responseDocument.Usage),
		ModelName:        firstNonEmptyDecisionValue(responseDocument.ModelName, decisionClient.ModelName),
		ProviderName:     firstNonEmptyDecisionValue(responseDocument.ProviderName, "capabilityLLM"),
		UpstreamProvider: responseDocument.UpstreamProvider,
		LatencyMS:        decisionLatency(responseDocument.LatencyMS, startedAt),
	}, nil
}

// The route answers a choice, a noul, or a score without naming which; the
// question that was asked is what says how to read it.
func typedDecisionAnswers(answers map[string]model.DecisionAnswer, questions map[string]model.DecisionQuestion) map[string]model.DecisionAnswer {
	typedAnswers := make(map[string]model.DecisionAnswer, len(answers))
	for questionName, answer := range answers {
		if answer.Type == "" {
			answer.Type = questions[questionName].Type
		}
		answer.Choice = strings.TrimSpace(answer.Choice)
		typedAnswers[questionName] = answer
	}
	return typedAnswers
}

type capabilityDecisionUsageDocument struct {
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CostUSD      float64 `json:"costUSD"`
}

func decisionUsage(usage capabilityDecisionUsageDocument) model.Usage {
	return model.Usage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.InputTokens + usage.OutputTokens,
		CostUSD:          usage.CostUSD,
	}
}

func decisionLatency(reportedLatencyMS int64, startedAt time.Time) int64 {
	if reportedLatencyMS > 0 {
		return reportedLatencyMS
	}
	return time.Since(startedAt).Milliseconds()
}

func firstNonEmptyDecisionValue(values ...string) string {
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
