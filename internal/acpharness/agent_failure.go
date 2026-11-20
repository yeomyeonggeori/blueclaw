package acpharness

import (
	"errors"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

type agentFailure struct {
	requestError *acp.RequestError
	reason       string
}

func (failure agentFailure) Error() string {
	return failure.reason
}

func (failure agentFailure) Unwrap() error {
	return failure.requestError
}

func agentFailureOf(errorValue error) error {
	var requestError *acp.RequestError
	if !errors.As(errorValue, &requestError) {
		return errorValue
	}
	reason := reasonCarriedBy(requestError)
	if reason == "" {
		return errorValue
	}
	return agentFailure{requestError: requestError, reason: reason}
}

func reasonCarriedBy(requestError *acp.RequestError) string {
	data, isObject := requestError.Data.(map[string]any)
	if !isObject {
		return ""
	}
	reason, _ := data["error"].(string)
	return strings.TrimSpace(reason)
}
