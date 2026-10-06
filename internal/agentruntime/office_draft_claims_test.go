//go:build !nobundledharness

package agentruntime

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const draftClaimsRequest = `{"claims":[{"path":"slides[0].units[0]","at":"slide 1 title","text":"Three bets for 2027"},{"path":"slides[0].units[1]","at":"slide 1 text","text":"Churn fell to 1.2 percent after the pilot."}]}`

func (fixture taskFixture) writeDraftClaimsRequest(t *testing.T, content string) {
	t.Helper()
	result := fixture.invoke(t, "bash", map[string]any{"command": `printf '%s' '` + content + `' > "$(dirname "$OFFICE_RUNTIME_CONTEXT")/` + officeContract.DraftClaims.RequestFile + `"`})
	if result.Failed() {
		t.Fatalf("the shell could not write the request: %s", result.ContentText())
	}
}

func TestTheHostJudgesTheClaimsOfADraftBeforeTheNextCommandAndAnswersInTheRuntimeContext(t *testing.T) {
	fixture := newTaskFixture(t)
	judge := &claimJudge{unsupportedText: "Churn fell to 1.2 percent after the pilot."}
	fixture.builder.UseClaimDecisionModel(judge)
	fixture.request.Prompt = "Make a strategy deck. Three bets for 2027."
	if told := string(fixture.contextTheShellReads(t)["judgesDraftClaims"]); told != "true" {
		t.Fatalf("a host with a claim judge told the office it judges drafts: %s", told)
	}

	fixture.writeDraftClaimsRequest(t, draftClaimsRequest)
	answer := fixture.contextTheShellReads(t)["draftClaims"]

	var draft struct {
		Digest      string `json:"digest"`
		Unsupported []struct {
			Path string `json:"path"`
			At   string `json:"at"`
			Text string `json:"text"`
		} `json:"unsupported"`
	}
	if errorValue := json.Unmarshal(answer, &draft); errorValue != nil {
		t.Fatalf("draftClaims is %s", answer)
	}
	if draft.Digest == "" || len(draft.Unsupported) != 1 || draft.Unsupported[0].Text != "Churn fell to 1.2 percent after the pilot." || draft.Unsupported[0].Path != "slides[0].units[1]" {
		t.Fatalf("draftClaims is %s", answer)
	}
}

func TestADraftTheHostAlreadyJudgedIsNotJudgedAgain(t *testing.T) {
	fixture := newTaskFixture(t)
	judge := &claimJudge{}
	fixture.builder.UseClaimDecisionModel(judge)
	fixture.writeDraftClaimsRequest(t, draftClaimsRequest)
	fixture.contextTheShellReads(t)
	callsAfterTheFirst := judge.calls

	fixture.contextTheShellReads(t)

	if judge.calls != callsAfterTheFirst || callsAfterTheFirst == 0 {
		t.Fatalf("the judge was called %d times after the first command and %d after the second", callsAfterTheFirst, judge.calls)
	}
}

func TestAHostWithoutAClaimJudgeNeitherJudgesNorAnnouncesIt(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.writeDraftClaimsRequest(t, draftClaimsRequest)
	document := fixture.contextTheShellReads(t)
	if string(document["judgesDraftClaims"]) != "false" || string(document["draftClaims"]) != "null" {
		t.Fatalf("judges %s, draft %s", document["judgesDraftClaims"], document["draftClaims"])
	}
}

func TestADraftWhoseAttachmentCouldNotBeReadIsJudgedUnsupportedOfNothing(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.request.VisibleContext = agentcontract.VisibleContext{CurrentMaterials: []agentcontract.VisibleContextMaterial{{MaterialID: "m-1", Filename: "scan.pdf", Path: "/inbox/scan.pdf", IsAvailable: true}}}
	fixture.builder.UseClaimDecisionModel(&claimJudge{unsupportedText: "Churn fell to 1.2 percent after the pilot."})
	fixture.writeDraftClaimsRequest(t, draftClaimsRequest)

	var draft struct {
		Unsupported []json.RawMessage `json:"unsupported"`
	}
	if errorValue := json.Unmarshal(fixture.contextTheShellReads(t)["draftClaims"], &draft); errorValue != nil || len(draft.Unsupported) != 0 {
		t.Fatalf("a draft whose attachment was unread was enforced: %+v, %v", draft, errorValue)
	}
}
