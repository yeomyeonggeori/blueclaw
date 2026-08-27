package connectors

import (
	"errors"
	"testing"
)

// Somebody attaches an image and asks for something to be done with it. When
// bringing it into the workspace fails, the agent used to be handed the
// attachment as if nothing had happened — a file with a url it cannot open —
// and would ask them to attach it again, which is what they had just done.
func TestARefusedAttachmentCarriesWhyItCouldNotBeRead(t *testing.T) {
	refused := connectorRefusedInputAttachments([]InputAttachment{{
		Platform: "buzz",
		URL:      "https://example.com/a-picture.png",
		Filename: "a-picture.png",
		Path:     "/workspace/private/people/somebody/tmp/a-picture.png",
	}}, errors.New("the messenger would not hand over the file"))

	if len(refused) != 1 {
		t.Fatalf("expected the attachment back, got %d", len(refused))
	}
	if refused[0].IsAvailable {
		t.Fatal("expected an attachment that could not be brought in to say so")
	}
	if refused[0].ErrorCode != connectorAttachmentImportRefusedCode {
		t.Fatalf("expected the refusal to be named, got %q", refused[0].ErrorCode)
	}
	if refused[0].Message != "the messenger would not hand over the file" {
		t.Fatalf("expected the reason to be carried, got %q", refused[0].Message)
	}
	if refused[0].Path != "" {
		t.Fatalf("expected no path to a file that was never written, got %q", refused[0].Path)
	}
}

// The refusal has to reach the agent in place of the attachment it is about,
// which means keeping whatever the replacement is keyed by.
func TestARefusedAttachmentReplacesTheOneItIsAbout(t *testing.T) {
	original := InputAttachment{Platform: "buzz", URL: "https://example.com/a-picture.png", Filename: "a-picture.png"}
	refused := connectorRefusedInputAttachments([]InputAttachment{original}, errors.New("no"))

	replaced := connectorReplaceImportedInputAttachments([]InputAttachment{original}, refused)

	if len(replaced) != 1 {
		t.Fatalf("expected one attachment, got %d", len(replaced))
	}
	if replaced[0].ErrorCode != connectorAttachmentImportRefusedCode {
		t.Fatal("expected the refusal to stand in for the attachment it is about")
	}
}
