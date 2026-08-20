package connectors

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func newResolverTestRuntime() *ConnectorRuntime {
	return &ConnectorRuntime{
		logger:          slog.New(slog.NewTextHandler(os.Stderr, nil)),
		identityService: identity.NewIdentityService(policy.PolicyProjection{}),
	}
}

type recordingUnknownAccountResolver struct {
	askedPlatform   string
	askedExternalID string
	askedEmail      string
	known           bool
	failure         error
}

func (resolver *recordingUnknownAccountResolver) ResolveUnknownAccount(_ context.Context, platform string, externalUserID string, email string) (bool, error) {
	resolver.askedPlatform = platform
	resolver.askedExternalID = externalUserID
	resolver.askedEmail = email
	return resolver.known, resolver.failure
}

func TestAHostThatCannotAnswerLeavesTheAccountUnmatched(t *testing.T) {
	runtime := newResolverTestRuntime()
	runtime.UseUnknownAccountResolver(&recordingUnknownAccountResolver{failure: errors.New("the directory is unreachable")})

	personID, isFound := runtime.askTheHostAboutUnknownAccount(context.Background(), "mattermost", "sender-1", "message-1", identity.PlatformAccountIdentity{Email: "이샘플@example.com"})

	if isFound || personID != "" {
		t.Fatal("a directory that cannot answer must not admit anyone, and it must not invent a person either")
	}
}

func TestAnAccountTheDirectoryDoesNotKnowStaysUnmatched(t *testing.T) {
	runtime := newResolverTestRuntime()
	runtime.UseUnknownAccountResolver(&recordingUnknownAccountResolver{known: false})

	if _, isFound := runtime.askTheHostAboutUnknownAccount(context.Background(), "mattermost", "sender-1", "message-1", identity.PlatformAccountIdentity{Email: "stranger@example.com"}); isFound {
		t.Fatal("someone the company directory does not carry is exactly who this refusal is for")
	}
}

func TestTheHostIsAskedWithWhatTheAccountPresented(t *testing.T) {
	resolver := &recordingUnknownAccountResolver{}
	runtime := newResolverTestRuntime()
	runtime.UseUnknownAccountResolver(resolver)

	runtime.askTheHostAboutUnknownAccount(context.Background(), "slack", "sender-7", "message-1", identity.PlatformAccountIdentity{Email: "이샘플@example.com"})

	if resolver.askedPlatform != "slack" || resolver.askedExternalID != "sender-7" || resolver.askedEmail != "이샘플@example.com" {
		t.Fatalf("the host answers about one account on one messenger, so all three have to reach it, got %q/%q/%q",
			resolver.askedPlatform, resolver.askedExternalID, resolver.askedEmail)
	}
}

func TestAHostWithNoResolverRefusesExactlyAsBefore(t *testing.T) {
	runtime := newResolverTestRuntime()

	if _, isFound := runtime.askTheHostAboutUnknownAccount(context.Background(), "mattermost", "sender-1", "message-1", identity.PlatformAccountIdentity{Email: "이샘플@example.com"}); isFound {
		t.Fatal("a deployment that wires no resolver has to behave as it did before this existed")
	}
}
