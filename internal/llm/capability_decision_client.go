package llm

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

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
	reportedWireExchange
}

func (decisionClient CapabilityDecisionClient) Decide(responseContext context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if decisionClient.CapabilityClient.HTTPClient == nil {
		return model.DecisionResponse{}, errors.New("capability decision http client is not configured")
	}
	if len(request.Questions) == 0 {
		return model.DecisionResponse{}, errors.New("a decision call carries no question")
	}
	requestDocument := capabilityDecisionRequestDocument{
		Model:     firstNonEmpty(request.Model, decisionClient.ModelName),
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
	responseDocument.record(responseContext)
	return model.DecisionResponse{
		Answers:          typedDecisionAnswers(responseDocument.Answers, request.Questions),
		Usage:            decisionUsage(responseDocument.Usage),
		ModelName:        firstNonEmpty(responseDocument.ModelName, decisionClient.ModelName),
		ProviderName:     firstNonEmpty(responseDocument.ProviderName, "capabilityLLM"),
		UpstreamProvider: responseDocument.UpstreamProvider,
		LatencyMS:        decisionLatency(responseDocument.LatencyMS, startedAt),
	}, nil
}

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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
