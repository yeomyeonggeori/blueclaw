package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const noticeSnapshot = `{"schema":"letter","given":{},"known":{"documentNumber":"SAMPLE-20261004-001"},"claims":[` +
	`{"path":"sections[0].blocks[0].text#0","at":"1. 이전 안내","text":"10월 20일 새 사무실로 이전합니다."},` +
	`{"path":"sections[0].blocks[0].text#1","at":"1. 이전 안내","text":"이전 기간에는 전화 응대가 어렵습니다."}]}`

type claimJudge struct {
	unsupportedText string
	calls           int
}

func (judge *claimJudge) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	judge.calls++
	state, _ := json.Marshal(request.State)
	var shown struct {
		Claims map[string]struct {
			Text string `json:"text"`
		} `json:"claims"`
	}
	_ = json.Unmarshal(state, &shown)
	answers := map[string]model.DecisionAnswer{}
	for key := range request.Questions {
		probability := 0.05
		if shown.Claims[key].Text == judge.unsupportedText {
			probability = 0.9
		}
		answers[key] = model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: "claim", Probabilities: map[string]float64{"claim": probability}}
	}
	return model.DecisionResponse{Answers: answers}, nil
}

func (fixture officeContextFixture) installOfficeStandIn(t *testing.T) string {
	t.Helper()
	recordPath := filepath.Join(fixture.workspacePath, "office-calls.txt")
	entryPath := filepath.Join(BundledSkillRootPath(fixture.workspacePath), "office", "scripts", "office")
	writeTestFile(t, entryPath, "#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+shellSingleQuoted(recordPath)+"\n")
	if errorValue := os.Chmod(entryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	return recordPath
}

func (fixture officeContextFixture) deliverWithJudge(t *testing.T, judge *claimJudge, name string) map[string]json.RawMessage {
	t.Helper()
	fixture.builder.UseClaimDecisionModel(judge)
	fixture.request.Prompt = "사무실 이전 안내문 만들어 줘. 10월 20일 새 사무실로 이전합니다."
	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/" + name})
	if result.Failed() || len(result.Attachments) != 1 {
		t.Fatalf("expected the file delivered, got %s", result.ContentText())
	}
	data := map[string]json.RawMessage{"content": json.RawMessage(MarshalBody(result.ContentText()))}
	_ = json.Unmarshal(result.Output.Data, &data)
	return data
}

func (fixture officeContextFixture) writeSnapshot(t *testing.T, name string, snapshot string) string {
	t.Helper()
	documentPath := fixture.writeDocument(t, name, false)
	writeTestFile(t, documentPath+officeContract.SourceSuffix, snapshot)
	return documentPath
}

func officeCalls(t *testing.T, recordPath string) []string {
	t.Helper()
	content, errorValue := os.ReadFile(recordPath)
	if os.IsNotExist(errorValue) {
		return nil
	}
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return strings.Split(strings.TrimSpace(string(content)), "\n")
}

func TestAnUnsupportedClaimIsBlankedByRemakingTheFileAsThePerson(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	recordPath := fixture.installOfficeStandIn(t)
	documentPath := fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)

	data := fixture.deliverWithJudge(t, &claimJudge{unsupportedText: "이전 기간에는 전화 응대가 어렵습니다."}, "notice.pdf")

	expected := []string{"merge", "letter", documentPath + officeContract.SourceSuffix, documentPath, "--blank", "sections[0].blocks[0].text#1"}
	if calls := officeCalls(t, recordPath); strings.Join(calls, " ") != strings.Join(expected, " ") {
		t.Fatalf("expected the remake %v, got %v", expected, calls)
	}
	if !strings.Contains(string(data["content"]), "1. 이전 안내") || !strings.Contains(string(data["claimChecks"]), `"outcome":"blanked"`) {
		t.Fatalf("expected the delivery to name the blank, got %s", data)
	}
}

func TestABlankedClaimIsANoteForTheReplyNamingWhatItSaid(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	fixture.builder.UseClaimDecisionModel(&claimJudge{unsupportedText: "이전 기간에는 전화 응대가 어렵습니다."})
	fixture.request.Prompt = "사무실 이전 안내문 만들어 줘. 10월 20일 새 사무실로 이전합니다."

	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/notice.pdf"})

	expected := []string{`notice.pdf: left blank because nothing the person gave supports them, for the reply to offer to complete: 1. 이전 안내 (it said "이전 기간에는 전화 응대가 어렵습니다.")`}
	if strings.Join(result.ReplyNotes, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("expected the reply note %q, got %q", expected, result.ReplyNotes)
	}
	if !strings.Contains(result.ContentText(), expected[0]) {
		t.Fatalf("expected the model to read the same note, got %s", result.ContentText())
	}
}

func TestASupportedFileHasNoNoteForTheReply(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	fixture.builder.UseClaimDecisionModel(&claimJudge{})

	if result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/notice.pdf"}); len(result.ReplyNotes) != 0 {
		t.Fatalf("expected no note, got %q", result.ReplyNotes)
	}
}

func TestASupportedFileIsDeliveredAsItIs(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	recordPath := fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)

	data := fixture.deliverWithJudge(t, &claimJudge{}, "notice.pdf")

	if calls := officeCalls(t, recordPath); calls != nil {
		t.Fatalf("expected no remake, got %v", calls)
	}
	if !strings.Contains(string(data["claimChecks"]), `"outcome":"supported"`) {
		t.Fatalf("expected the check recorded, got %s", data)
	}
}

func TestAClaimCopiedFromTheRequestIsNotAsked(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)

	data := fixture.deliverWithJudge(t, &claimJudge{}, "notice.pdf")

	if !strings.Contains(string(data["claimChecks"]), `"asked":1`) {
		t.Fatalf("expected only the uncopied sentence asked, got %s", data["claimChecks"])
	}
}

func TestADeckIsRebuiltFromItsSourceWithTheBlank(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	recordPath := fixture.installOfficeStandIn(t)
	deckPath := filepath.Join(fixture.homePath(), "artifacts", "deck", "slides.html")
	documentPath := fixture.writeSnapshot(t, "deck.pdf", `{"command":"office create","deck":"`+deckPath+`","claims":[{"path":"slides[1].units[2]","at":"슬라이드 2 항목","text":"국내 시장 점유율 1위"}]}`)

	fixture.deliverWithJudge(t, &claimJudge{unsupportedText: "국내 시장 점유율 1위"}, "deck.pdf")

	expected := []string{"create", documentPath, deckPath, "--blank", "slides[1].units[2]"}
	if calls := officeCalls(t, recordPath); strings.Join(calls, " ") != strings.Join(expected, " ") {
		t.Fatalf("expected the deck rebuilt %v, got %v", expected, calls)
	}
}

func TestAFileFromBeforeTheTaskIsNotJudgedAgainstThisRequest(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	fixture.installOfficeStandIn(t)
	documentPath := fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	earlier := fixture.taskRun.CreatedAt.Add(-time.Hour)
	if errorValue := os.Chtimes(documentPath, earlier, earlier); errorValue != nil {
		t.Fatal(errorValue)
	}
	judge := &claimJudge{unsupportedText: "이전 기간에는 전화 응대가 어렵습니다."}

	data := fixture.deliverWithJudge(t, judge, "notice.pdf")

	if judge.calls != 0 || data["claimChecks"] != nil {
		t.Fatalf("expected an earlier task's file left alone, got %d calls and %s", judge.calls, data["claimChecks"])
	}
}

func TestAClaimIsNotBlankedWhenAnAttachmentCouldNotBeRead(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	recordPath := fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	fixture.request.VisibleContext = agentcontract.VisibleContext{CurrentMaterials: []agentcontract.VisibleContextMaterial{{MaterialID: "m-1", Filename: "scan.pdf", IsAvailable: true}}}

	data := fixture.deliverWithJudge(t, &claimJudge{unsupportedText: "이전 기간에는 전화 응대가 어렵습니다."}, "notice.pdf")

	if calls := officeCalls(t, recordPath); calls != nil {
		t.Fatalf("expected no remake while an attachment is unread, got %v", calls)
	}
	if !strings.Contains(string(data["claimChecks"]), claimOutcomeUnreadSources) {
		t.Fatalf("expected the reason recorded, got %s", data["claimChecks"])
	}
}

func TestASnapshotThePersonCannotReadIsNotJudged(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	documentPath := fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	snapshotPath := documentPath + officeContract.SourceSuffix
	if errorValue := os.Chmod(snapshotPath, 0o000); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.Chmod(snapshotPath, 0o600) })
	judge := &claimJudge{}

	fixture.deliverWithJudge(t, judge, "notice.pdf")

	if judge.calls != 0 {
		t.Fatal("expected the snapshot read to follow the person's file permissions")
	}
}
