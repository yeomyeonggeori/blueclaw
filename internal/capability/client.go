package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const DefaultEndpoint = "http://127.0.0.1:7781"
const DefaultSocketEndpoint = "http://capability"

type Configuration struct {
	Endpoint       string
	Transport      string
	UnixSocketPath string
	VSockCID       uint32
	VSockPort      uint32
	Timeout        time.Duration
}

type Client struct {
	Endpoint   string
	HTTPClient HTTPDoer
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func NewClient(configuration Configuration) Client {
	endpoint, httpClient := Dial(configuration)
	return Client{
		Endpoint:   endpoint,
		HTTPClient: httpClient,
	}
}

func Dial(configuration Configuration) (string, *http.Client) {
	endpoint := strings.TrimRight(strings.TrimSpace(configuration.Endpoint), "/")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	httpClient := &http.Client{}
	if configuration.Timeout > 0 {
		httpClient.Timeout = configuration.Timeout
	}

	unixSocketPath := strings.TrimSpace(configuration.UnixSocketPath)
	if strings.TrimSpace(configuration.Transport) == "vsock" {
		transport, errorValue := newVSockTransport(configuration.VSockCID, configuration.VSockPort)
		httpClient.Transport = transportWithError(transport, errorValue)
		if strings.TrimSpace(configuration.Endpoint) == "" {
			endpoint = DefaultSocketEndpoint
		}
	} else if unixSocketPath != "" {
		httpClient.Transport = newUnixSocketTransport(unixSocketPath)
		if strings.TrimSpace(configuration.Endpoint) == "" {
			endpoint = DefaultSocketEndpoint
		}
	}

	return endpoint, httpClient
}

func transportWithError(transport *http.Transport, errorValue error) http.RoundTripper {
	if errorValue != nil {
		return roundTripError{errorValue: errorValue}
	}
	return transport
}

type roundTripError struct {
	errorValue error
}

func (roundTripper roundTripError) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, roundTripper.errorValue
}

func (client Client) PostJSON(ctx context.Context, path string, requestDocument any, responseDocument any) error {
	if client.HTTPClient == nil {
		return errors.New("capability http client is not configured")
	}

	requestBody, errorValue := json.Marshal(requestDocument)
	if errorValue != nil {
		return errorValue
	}

	httpRequest, errorValue := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.endpointURL(path),
		bytes.NewReader(requestBody),
	)
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

func (client Client) GetJSON(ctx context.Context, path string, responseDocument any) error {
	if client.HTTPClient == nil {
		return errors.New("capability http client is not configured")
	}

	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodGet, client.endpointURL(path), nil)
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

	if responseDocument == nil || len(responseBody) == 0 {
		return nil
	}

	return json.Unmarshal(responseBody, responseDocument)
}

func (client Client) endpointURL(path string) string {
	cleanPath := "/" + strings.TrimLeft(path, "/")
	return strings.TrimRight(client.Endpoint, "/") + cleanPath
}

func newUnixSocketTransport(unixSocketPath string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{}
			return dialer.DialContext(ctx, "unix", unixSocketPath)
		},
	}
}
