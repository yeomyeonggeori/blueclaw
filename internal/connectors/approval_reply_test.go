package connectors

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
)

func TestAPlainApprovalIsOfferedAsApproveAndReject(t *testing.T) {
	question := approvalQuestionFor("보낼까요?", nil)

	if len(question.Options) != 2 || question.Options[0].ID != ApproveOptionID || question.Options[1].ID != RejectOptionID || question.Options[1].Meaning != approvalreply.RejectMeaning {
		t.Fatalf("expected approve and reject, got %+v", question.Options)
	}
}

func TestAChoiceQuestionOffersEachChoiceAndCancelAsDeclining(t *testing.T) {
	choices := []approvalgate.ApprovalChoice{{Key: "now"}, {Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}}

	question := approvalQuestionFor("언제 할까요?", choices)

	if len(question.Options) != 3 || question.Options[0].ID != "now" || question.Options[1].ID != "offHours" || question.Options[2].ID != approvalgate.CancelChoiceKey {
		t.Fatalf("expected the choices and cancel, got %+v", question.Options)
	}
	if question.Options[2].Meaning != approvalreply.RejectMeaning || question.Options[1].Meaning == approvalreply.RejectMeaning {
		t.Fatalf("expected only cancel to mean declining, got %+v", question.Options)
	}
}
