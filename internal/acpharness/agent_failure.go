package acpharness

import (
	"errors"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/acpfailure"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
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

func (failure agentFailure) FailureCause() agentcontract.FailureCause {
	return acpfailure.CauseOf(failure.requestError)
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
