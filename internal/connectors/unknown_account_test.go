package connectors

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
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

	personID, isFound, directoryUnreachable := runtime.askTheHostAboutUnknownAccount(context.Background(), "mattermost", "sender-1", "message-1", identity.PlatformAccountIdentity{Email: "이샘플@example.com"})

	if isFound || personID != "" {
		t.Fatal("a directory that cannot answer must not admit anyone, and it must not invent a person either")
	}
	if !directoryUnreachable {
		t.Fatal("a directory that cannot answer has not said this account is unknown")
	}
}

func TestAnAccountTheDirectoryDoesNotKnowStaysUnmatched(t *testing.T) {
	runtime := newResolverTestRuntime()
	runtime.UseUnknownAccountResolver(&recordingUnknownAccountResolver{known: false})

	_, isFound, directoryUnreachable := runtime.askTheHostAboutUnknownAccount(context.Background(), "mattermost", "sender-1", "message-1", identity.PlatformAccountIdentity{Email: "stranger@example.com"})

	if isFound {
		t.Fatal("someone the company directory does not carry is exactly who this refusal is for")
	}
	if directoryUnreachable {
		t.Fatal("the directory answered, and what it said was no")
	}
}

func TestTheHostIsAskedWithWhatTheAccountPresented(t *testing.T) {
	resolver := &recordingUnknownAccountResolver{}
	runtime := newResolverTestRuntime()
	runtime.UseUnknownAccountResolver(resolver)

	_, _, _ = runtime.askTheHostAboutUnknownAccount(context.Background(), "slack", "sender-7", "message-1", identity.PlatformAccountIdentity{Email: "이샘플@example.com"})

	if resolver.askedPlatform != "slack" || resolver.askedExternalID != "sender-7" || resolver.askedEmail != "이샘플@example.com" {
		t.Fatalf("the host answers about one account on one messenger, so all three have to reach it, got %q/%q/%q",
			resolver.askedPlatform, resolver.askedExternalID, resolver.askedEmail)
	}
}

func TestAHostWithNoResolverRefusesExactlyAsBefore(t *testing.T) {
	runtime := newResolverTestRuntime()

	_, isFound, directoryUnreachable := runtime.askTheHostAboutUnknownAccount(context.Background(), "mattermost", "sender-1", "message-1", identity.PlatformAccountIdentity{Email: "이샘플@example.com"})

	if isFound {
		t.Fatal("a deployment that wires no resolver has to behave as it did before this existed")
	}
	if directoryUnreachable {
		t.Fatal("a deployment with no directory has none to fail to reach")
	}
}

// The two refusals read the same to whoever receives them unless they are told
// apart, and today the ledger recorded a lookup that failed as an account
// nobody carries.
func TestADirectoryThatNeverAnsweredIsNotToldAsAnUnknownAccount(t *testing.T) {
	unreachable := unmatchedAccountReplyFor(senderAuthorization{
		Platform:             "buzz",
		PlatformAccountEmail: "lee@example.test",
		DirectoryUnreachable: true,
	}, "en")
	answeredNo := unmatchedAccountReplyFor(senderAuthorization{
		Platform:             "buzz",
		PlatformAccountEmail: "lee@example.test",
	}, "en")

	if unreachable == answeredNo {
		t.Fatal("a lookup that failed and an answer of no cannot read the same")
	}
	if strings.Contains(unreachable, "no one here is on file") {
		t.Fatalf("nobody was asked, so nobody can be said to be missing, got %q", unreachable)
	}
	if !strings.Contains(answeredNo, "lee@example.test") {
		t.Fatalf("a refusal that does not name the address leaves everyone guessing, got %q", answeredNo)
	}
}

// The directory carries the address and this agent carries no person under it,
// which is a projection that has not caught up rather than a stranger.
func TestAnAddressTheDirectoryCarriesIsNotRefusedAsUnknown(t *testing.T) {
	runtime := newResolverTestRuntime()
	runtime.UseUnknownAccountResolver(&recordingUnknownAccountResolver{known: true})

	_, isFound, directoryUnreachable := runtime.askTheHostAboutUnknownAccount(context.Background(), "buzz", "sender-9", "message-1", identity.PlatformAccountIdentity{Email: "known@example.test"})

	if isFound {
		t.Fatal("this runtime carries no person under that address, so none can be found")
	}
	if !directoryUnreachable {
		t.Fatal("the directory said yes and this agent could not place it, which nobody should hear as being unknown")
	}
}
