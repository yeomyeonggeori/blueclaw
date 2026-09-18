package connectors

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

type UnknownAccountResolver interface {
	ResolveUnknownAccount(ctx context.Context, platform string, externalUserID string, email string) (bool, error)
}

type capabilityUnknownAccountResolver struct {
	capabilityClient capability.Client
}

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
