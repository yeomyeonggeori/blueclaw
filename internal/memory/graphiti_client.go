package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type GraphitiClient struct {
	Endpoint   string
	HTTPClient HTTPDoer
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type graphitiSearchResponse struct {
	Facts []MemoryFact `json:"facts"`
}

func NewGraphitiClient(endpoint string, timeout time.Duration) GraphitiClient {
	cleanEndpoint := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return GraphitiClient{
		Endpoint:   cleanEndpoint,
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

func (client GraphitiClient) AddEpisode(ctx context.Context, episode MemoryEpisode) (MemoryIngestionResult, error) {
	var response MemoryIngestionResult
	errorValue := client.post(ctx, "/v1/episodes", episode, &response)
	if errorValue != nil {
		return MemoryIngestionResult{}, errorValue
	}
	return response, nil
}

func (client GraphitiClient) DeleteEpisode(ctx context.Context, request MemoryEpisodeDeleteRequest) (MemoryEpisodeDeleteResult, error) {
	var response MemoryEpisodeDeleteResult
	errorValue := client.post(ctx, "/v1/episodes/delete", request, &response)
	if errorValue != nil {
		return MemoryEpisodeDeleteResult{}, errorValue
	}
	return response, nil
}

func (client GraphitiClient) UpdateFact(ctx context.Context, request MemoryFactUpdateRequest) (MemoryFact, error) {
	var response MemoryFact
	if errorValue := client.post(ctx, "/v1/facts/update", request, &response); errorValue != nil {
		return MemoryFact{}, errorValue
	}
	return response, nil
}

func (client GraphitiClient) DeleteFact(ctx context.Context, request MemoryFactDeleteRequest) (MemoryFactMutationResult, error) {
	var response MemoryFactMutationResult
	if errorValue := client.post(ctx, "/v1/facts/delete", request, &response); errorValue != nil {
		return MemoryFactMutationResult{}, errorValue
	}
	return response, nil
}

func (client GraphitiClient) SearchFacts(ctx context.Context, request MemorySearchRequest) ([]MemoryFact, error) {
	var response graphitiSearchResponse
	errorValue := client.post(ctx, "/v1/search", request, &response)
	if errorValue != nil {
		return nil, errorValue
	}
	return response.Facts, nil
}

func (client GraphitiClient) ListFacts(ctx context.Context, request MemorySearchRequest) ([]MemoryFact, error) {
	var response graphitiSearchResponse
	errorValue := client.post(ctx, "/v1/list", request, &response)
	if errorValue != nil {
		return nil, errorValue
	}
	return response.Facts, nil
}

func (client GraphitiClient) post(ctx context.Context, path string, requestDocument any, responseDocument any) error {
	if client.HTTPClient == nil {
		return errors.New("graphiti http client is not configured")
	}
	requestBody, errorValue := json.Marshal(requestDocument)
	if errorValue != nil {
		return errorValue
	}
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, client.endpointURL(path), bytes.NewReader(requestBody))
	if errorValue != nil {
		return errorValue
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, errorValue := client.HTTPClient.Do(httpRequest)
	if errorValue != nil {
		return errorValue
	}
	defer httpResponse.Body.Close()

	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return errorValue
	}
	if httpResponse.StatusCode >= http.StatusBadRequest {
		return errors.New(strings.TrimSpace(string(responseBody)))
	}
	if responseDocument == nil || len(responseBody) == 0 {
		return nil
	}
	return json.Unmarshal(responseBody, responseDocument)
}

func (client GraphitiClient) CheckHealth(ctx context.Context) error {
	if client.HTTPClient == nil {
		return errors.New("graphiti http client is not configured")
	}
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodGet, client.endpointURL("/health"), nil)
	if errorValue != nil {
		return errorValue
	}
	httpResponse, errorValue := client.HTTPClient.Do(httpRequest)
	if errorValue != nil {
		return errorValue
	}
	defer httpResponse.Body.Close()
	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return errorValue
	}
	if httpResponse.StatusCode >= http.StatusBadRequest {
		return errors.New(strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func (client GraphitiClient) endpointURL(path string) string {
	return strings.TrimRight(client.Endpoint, "/") + "/" + strings.TrimLeft(path, "/")
}
