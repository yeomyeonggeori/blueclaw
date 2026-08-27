package approvalgate

import "testing"

// The reason an agent gives for needing an approval states where it is heading,
// not what this call does. Handing it over as the action's content is how a
// download to a temporary file became "shall I translate this and post it to
// 잡담", which the person then approved and the runtime did not do.
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
