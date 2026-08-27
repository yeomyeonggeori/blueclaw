package connectors

import "testing"

func TestInputAttachmentKeyIdentifiesURLOnlyAttachments(t *testing.T) {
	byURL := InputAttachment{Platform: "buzz", URL: "https://relay.example.test/media/abc.png"}
	if connectorInputAttachmentKey(byURL) != "buzz:https://relay.example.test/media/abc.png" {
		t.Fatalf("unexpected key %q", connectorInputAttachmentKey(byURL))
	}
	imported := byURL
	imported.Path = "/workspace/inbox/buzz/잡담/abc.png"
	if connectorInputAttachmentKey(imported) != connectorInputAttachmentKey(byURL) {
		t.Fatal("an imported attachment must keep the identity it was requested by")
	}
}

func TestReplaceImportedInputAttachmentsMatchesByURL(t *testing.T) {
	requested := []InputAttachment{{Platform: "buzz", URL: "https://relay.example.test/media/abc.png", Filename: "지도.png"}}
	imported := []InputAttachment{{
		Platform:    "buzz",
		URL:         "https://relay.example.test/media/abc.png",
		Filename:    "지도.png",
		Path:        "/workspace/inbox/buzz/잡담/지도.png",
		IsAvailable: true,
	}}

	replaced := connectorReplaceImportedInputAttachments(requested, imported)

	if len(replaced) != 1 {
		t.Fatalf("expected one attachment, got %d", len(replaced))
	}
	if replaced[0].Path != "/workspace/inbox/buzz/잡담/지도.png" || !replaced[0].IsAvailable {
		t.Fatalf("imported attachment did not replace the requested one: %+v", replaced[0])
	}
}
