package approvalgate

import "testing"

// The reason an agent gives for needing an approval states where it is heading,
// not what this call does. Handing it over as the action's content is how a
// download to a temporary file became "shall I translate this and post it to
// 잡담", which the person then approved and the runtime did not do.
// An approval question is the user's only view of the action, so the wording
// model must be handed the exact span an edit replaces and what the removal
// targets actually say — not just their IDs.
func TestAnEditsSpanAndAPreviewReachTheWordingModel(t *testing.T) {
	toolInput := []byte(`{"messageID":"m-1","oldText":"금요일","newText":"목요일"}`)

	details := approvalQuestionActionDetails(toolInput, ApprovalTarget{Preview: "회식은 금요일입니다"})

	if details["replacedText"] != "금요일" || details["replacementText"] != "목요일" {
		t.Fatalf("expected the edited span to travel, got %q -> %q", details["replacedText"], details["replacementText"])
	}
	if details["targetPreview"] != "회식은 금요일입니다" {
		t.Fatalf("expected the target's own words to travel, got %q", details["targetPreview"])
	}
}

func TestTheReasonForAnActionIsNotItsContent(t *testing.T) {
	toolInput := []byte(`{"command":"curl -L https://example.test/a.jpg","approvalReason":"내용을 확인하고 번역해 게시하기 위해 필요합니다"}`)

	details := approvalQuestionActionDetails(toolInput, ApprovalTarget{})

	if details["content"] != "" {
		t.Fatalf("expected the reason to stay out of the content, got %q", details["content"])
	}
	if details["reason"] != "내용을 확인하고 번역해 게시하기 위해 필요합니다" {
		t.Fatalf("expected the reason to be kept as a reason, got %q", details["reason"])
	}
}
