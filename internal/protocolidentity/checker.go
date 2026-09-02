package protocolidentity

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Identity struct {
	ProtocolVersion       string `json:"protocolVersion"`
	AggregateProtocolHash string `json:"aggregateProtocolHash"`
}

type EndpointStatus struct {
	Status                string `json:"status"`
	Passed                bool   `json:"passed"`
	ProtocolVersion       string `json:"protocolVersion,omitempty"`
	AggregateProtocolHash string `json:"aggregateProtocolHash,omitempty"`
	Error                 string `json:"error,omitempty"`
}

type Result struct {
	Passed         bool           `json:"passed"`
	Expected       Identity       `json:"expected"`
	Capabilityd    EndpointStatus `json:"capabilityd"`
	FailureReasons []string       `json:"failureReasons,omitempty"`
	CheckedAt      time.Time      `json:"checkedAt"`
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Configuration struct {
	CapabilityEndpoint   string
	Timeout              time.Duration
	HTTPClient           HTTPDoer
	CapabilityHTTPClient HTTPDoer
}

type Checker struct {
	capabilityEndpoint   string
	timeout              time.Duration
	capabilityHTTPClient HTTPDoer
}

func NewChecker(configuration Configuration) Checker {
	timeout := configuration.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	defaultHTTPClient := configuration.HTTPClient
	if defaultHTTPClient == nil {
		defaultHTTPClient = http.DefaultClient
	}
	capabilityHTTPClient := configuration.CapabilityHTTPClient
	if capabilityHTTPClient == nil {
		capabilityHTTPClient = defaultHTTPClient
	}
	return Checker{
		capabilityEndpoint:   strings.TrimRight(strings.TrimSpace(configuration.CapabilityEndpoint), "/"),
		timeout:              timeout,
		capabilityHTTPClient: capabilityHTTPClient,
	}
}

func ValidateIdentity(identity Identity) error {
	if identity.ProtocolVersion == "" || identity.ProtocolVersion != strings.TrimSpace(identity.ProtocolVersion) {
		return errors.New("protocol version must be a non-empty trimmed string")
	}
	if identity.AggregateProtocolHash != strings.TrimSpace(identity.AggregateProtocolHash) {
		return errors.New("aggregate protocol hash must be a 64-character lowercase hexadecimal hash")
	}
	if len(identity.AggregateProtocolHash) != 64 {
		return errors.New("aggregate protocol hash must be a 64-character lowercase hexadecimal hash")
	}
	if _, errorValue := hex.DecodeString(identity.AggregateProtocolHash); errorValue != nil || strings.ToLower(identity.AggregateProtocolHash) != identity.AggregateProtocolHash {
		return errors.New("aggregate protocol hash must be a 64-character lowercase hexadecimal hash")
	}
	return nil
}

func (checker Checker) Check(ctx context.Context, expected Identity) Result {
	result := Result{Expected: expected, CheckedAt: time.Now().UTC()}
	if errorValue := ValidateIdentity(expected); errorValue != nil {
		result.FailureReasons = append(result.FailureReasons, "expected identity is invalid: "+errorValue.Error())
		result.Capabilityd = unavailableStatus(errorValue)
		return result
	}
	requestContext, cancel := context.WithTimeout(ctx, checker.timeout)
	defer cancel()
	result.Capabilityd = checker.checkOptionalEndpoint(requestContext, checker.capabilityEndpoint, "/v1/capabilities", expected, checker.capabilityHTTPClient)
	result.FailureReasons = append(result.FailureReasons, endpointFailureReason("capabilityd", result.Capabilityd))
	result.FailureReasons = compactFailureReasons(result.FailureReasons)
	result.Passed = result.Capabilityd.Passed
	return result
}

func (checker Checker) checkOptionalEndpoint(ctx context.Context, endpoint string, path string, expected Identity, httpClient HTTPDoer) EndpointStatus {
	if strings.TrimSpace(endpoint) == "" {
		return EndpointStatus{Status: "not_configured", Passed: true}
	}
	return checker.checkEndpoint(ctx, endpoint, path, expected, httpClient)
}

func (checker Checker) checkEndpoint(ctx context.Context, endpoint string, path string, expected Identity, httpClient HTTPDoer) EndpointStatus {
	responseDocument := Identity{}
	if errorValue := getJSON(ctx, endpoint, path, &responseDocument, httpClient); errorValue != nil {
		return unavailableStatus(errorValue)
	}
	status := EndpointStatus{
		ProtocolVersion:       responseDocument.ProtocolVersion,
		AggregateProtocolHash: responseDocument.AggregateProtocolHash,
	}
	if responseDocument != expected {
		status.Status = "drift"
		status.Error = fmt.Sprintf(
			"expected protocolVersion %q and aggregateProtocolHash %q, received protocolVersion %q and aggregateProtocolHash %q",
			expected.ProtocolVersion, expected.AggregateProtocolHash,
			responseDocument.ProtocolVersion, responseDocument.AggregateProtocolHash,
		)
		return status
	}
	status.Status = "ok"
	status.Passed = true
	return status
}

func getJSON(ctx context.Context, endpoint string, path string, target any, httpClient HTTPDoer) error {
	requestURL, errorValue := endpointURL(endpoint, path)
	if errorValue != nil {
		return errorValue
	}
	request, errorValue := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if errorValue != nil {
		return errorValue
	}
	response, errorValue := httpClient.Do(request)
	if errorValue != nil {
		return errorValue
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("http status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1024*1024))
	if errorValue := decoder.Decode(target); errorValue != nil {
		return errorValue
	}
	if errorValue := decoder.Decode(&struct{}{}); errorValue != io.EOF {
		return errors.New("response contains multiple JSON values")
	}
	return nil
}

func endpointURL(endpoint string, path string) (string, error) {
	if strings.TrimSpace(endpoint) == "" {
		return "", errors.New("endpoint is not configured")
	}
	parsedURL, errorValue := url.Parse(endpoint)
	if errorValue != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return "", errors.New("endpoint is invalid")
	}
	parsedURL.Path = strings.TrimRight(parsedURL.Path, "/") + path
	parsedURL.RawPath = ""
	return parsedURL.String(), nil
}

func unavailableStatus(errorValue error) EndpointStatus {
	return EndpointStatus{Status: "unavailable", Error: errorValue.Error()}
}

func endpointFailureReason(name string, status EndpointStatus) string {
	if status.Passed {
		return ""
	}
	if status.Error == "" {
		return name + " protocol identity check failed"
	}
	return name + ": " + status.Error
}

func compactFailureReasons(reasons []string) []string {
	compactedReasons := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason != "" {
			compactedReasons = append(compactedReasons, reason)
		}
	}
	return compactedReasons
}
