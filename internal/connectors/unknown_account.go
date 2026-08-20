package connectors

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

// UnknownAccountResolver asks the host whether an account this agent cannot match
// belongs to somebody at the company. The host owns that question: it reads the
// account directory and leaves this agent carrying the person, so the message that
// prompted the question is served rather than refused.
//
// This sits at the one place a match fails, not on each platform adapter. Whether a
// message arrived through chatd or through a capability is a routing detail, and an
// answer that depends on which door it came through is the same fact kept twice.
type UnknownAccountResolver interface {
	ResolveUnknownAccount(ctx context.Context, platform string, externalUserID string, email string) (bool, error)
}

type capabilityUnknownAccountResolver struct {
	capabilityClient capability.Client
}

// NewCapabilityUnknownAccountResolver reaches the host over the capability socket,
// which every deployment has regardless of which adapter carries its messengers.
func NewCapabilityUnknownAccountResolver(capabilityClient capability.Client) UnknownAccountResolver {
	return capabilityUnknownAccountResolver{capabilityClient: capabilityClient}
}

type unknownAccountRequest struct {
	Platform       string `json:"platform"`
	ExternalUserID string `json:"externalUserID"`
	Email          string `json:"email"`
}

type unknownAccountResponse struct {
	Known bool `json:"known"`
}

func (resolver capabilityUnknownAccountResolver) ResolveUnknownAccount(ctx context.Context, platform string, externalUserID string, email string) (bool, error) {
	var response unknownAccountResponse
	errorValue := resolver.capabilityClient.PostJSON(ctx, "/v1/directory/person", unknownAccountRequest{
		Platform:       strings.TrimSpace(platform),
		ExternalUserID: strings.TrimSpace(externalUserID),
		Email:          strings.TrimSpace(email),
	}, &response)
	if errorValue != nil {
		return false, errorValue
	}
	return response.Known, nil
}
