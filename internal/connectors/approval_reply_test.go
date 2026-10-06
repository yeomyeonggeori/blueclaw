package connectors

import (
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
)

func TestAPlainApprovalIsOfferedAsApproveAndReject(t *testing.T) {
	question := approvalQuestionFor("보낼까요?", nil)

	if len(question.Options) != 2 || question.Options[0].ID != ApproveOptionID || question.Options[1].ID != RejectOptionID || question.Options[1].Meaning != approvalreply.RejectMeaning {
		t.Fatalf("expected approve and reject, got %+v", question.Options)
	}
}

func TestAChoiceQuestionOffersEachChoiceAndCancelAsDeclining(t *testing.T) {
	choices := []holdrecord.Choice{{Key: "now"}, {Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}}

	question := approvalQuestionFor("언제 할까요?", choices)

	if len(question.Options) != 3 || question.Options[0].ID != "now" || question.Options[1].ID != "offHours" || question.Options[2].ID != approvalrecord.CancelChoiceKey {
		t.Fatalf("expected the choices and cancel, got %+v", question.Options)
	}
	if question.Options[2].Meaning != approvalreply.RejectMeaning || question.Options[1].Meaning == approvalreply.RejectMeaning {
		t.Fatalf("expected only cancel to mean declining, got %+v", question.Options)
	}
}

func TestAnAnswerOptionIsOfferedToTheReaderUnderItsOwnWords(t *testing.T) {
	choices := []holdrecord.Choice{{Key: "1", Label: "회의실 A"}, {Key: "2", Label: "회의실 B"}}

	question := approvalQuestionFor("어느 방으로 할까요?", choices)

	if len(question.Options) != 3 || question.Options[1].ID != "2" || question.Options[1].Meaning != approvalreply.AllowMeaning("회의실 B") {
		t.Fatalf("the reader reads the person's words against the options' own words, got %+v", question.Options)
	}
}
