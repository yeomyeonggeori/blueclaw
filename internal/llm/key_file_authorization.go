package llm

import (
	"context"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/model/decisions"
)

func keyFilePath(endpointConfiguration config.ModelEndpointConfiguration) string {
	if strings.TrimSpace(endpointConfiguration.APIKeyEnvironment) != "" {
		return ""
	}
	return strings.TrimSpace(endpointConfiguration.APIKeyPath)
}

func keyFileClient(apiKeyPath string, requesterEmailHeader string) *http.Client {
	return &http.Client{Transport: keyFileAuthorization{apiKeyPath: apiKeyPath, requesterEmailHeader: strings.TrimSpace(requesterEmailHeader)}}
}

type keyFileAuthorization struct {
	apiKeyPath           string
	requesterEmailHeader string
}

func (authorization keyFileAuthorization) RoundTrip(request *http.Request) (*http.Response, error) {
	apiKey, errorValue := readAPIKey(authorization.apiKeyPath)
	if errorValue != nil {
		if request.Body != nil {
			request.Body.Close()
		}
		return nil, errorValue
	}
	authorized := request.Clone(request.Context())
	authorized.Header.Set("Authorization", "Bearer "+apiKey)
	nameRequester(authorized, authorization.requesterEmailHeader)
	return http.DefaultTransport.RoundTrip(authorized)
}

type keyFileDecisionModel struct {
	endpointURL          string
	modelName            string
	apiKeyPath           string
	requesterEmailHeader string
}

func (decisionModel keyFileDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	apiKey, errorValue := readAPIKey(decisionModel.apiKeyPath)
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	endpoint := decisions.Endpoint{URL: decisionModel.endpointURL, ModelName: decisionModel.modelName, APIKey: apiKey, HTTPClient: requesterNamingDecisionClient(decisionModel.requesterEmailHeader)}
	return endpoint.DecisionModel().Decide(ctx, request)
}
