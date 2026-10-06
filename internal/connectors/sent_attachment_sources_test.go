package connectors

import (
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func directMessageResolverForPerson(personID string, store *sentAttachmentSourceStore) connectorAttachmentMaterialResolver {
	return connectorAttachmentMaterialResolver{
		personID: personID,
		event: PlatformInboundEvent{
			Platform:       "mattermost",
			ConversationID: "conversation-1",
			Context:        VisibleContext{ConversationType: "D"},
		},
		sentSources: store,
	}
}

func TestSentSourceMaterialResolvesDeliveredAttachmentToItsWorkspaceSource(t *testing.T) {
	store := newSentAttachmentSourceStore()
	store.RecordReply("mattermost", "post-1", []toolcontract.FileAttachment{{
		DevicePath: "/workspace/private/people/person-1/customer-support-weekly-check.json",
		Filename:   "customer-support-weekly-check.json",
	}})
	resolver := directMessageResolverForPerson("person-1", store)

	material, isResolved := resolver.sentSourceMaterial(InputAttachment{
		Platform:  "mattermost",
		MessageID: "post-1",
		FileID:    "file-1",
		Filename:  "customer-support-weekly-check.json",
	})

	if !isResolved {
		t.Fatal("expected the delivered attachment to resolve to its workspace source")
	}
	if material.Path != "~/customer-support-weekly-check.json" {
		t.Fatalf("expected the requester-readable source path, got %q", material.Path)
	}
}

func TestSentSourceMaterialKeepsImportForOtherOwnersAndUnknownAttachments(t *testing.T) {
	store := newSentAttachmentSourceStore()
	store.RecordReply("mattermost", "post-1", []toolcontract.FileAttachment{{
		DevicePath: "/workspace/private/people/person-2/report.json",
		Filename:   "report.json",
	}})
	resolver := directMessageResolverForPerson("person-1", store)

	if _, isResolved := resolver.sentSourceMaterial(InputAttachment{
		Platform: "mattermost", MessageID: "post-1", Filename: "report.json",
	}); isResolved {
		t.Fatal("expected a source outside the requester's home to keep the import path")
	}
	if _, isResolved := resolver.sentSourceMaterial(InputAttachment{
		Platform: "mattermost", MessageID: "post-9", Filename: "report.json",
	}); isResolved {
		t.Fatal("expected an unrecorded attachment to keep the import path")
	}
}

func TestRecordReplySkipsAmbiguousFilenames(t *testing.T) {
	store := newSentAttachmentSourceStore()
	store.RecordReply("mattermost", "post-1", []toolcontract.FileAttachment{
		{DevicePath: "/workspace/private/people/person-1/a/report.json", Filename: "report.json"},
		{DevicePath: "/workspace/private/people/person-1/b/report.json", Filename: "report.json"},
	})

	if _, isFound := store.SourcePath("mattermost", "post-1", "report.json"); isFound {
		t.Fatal("expected an ambiguous filename to stay unrecorded")
	}
}
