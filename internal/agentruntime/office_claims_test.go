//go:build !nobundledharness

package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/officeclaimcheck"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

const noticeSnapshot = `{"schema":"letter","given":{},"known":{"documentNumber":"SAMPLE-20261004-001"},"claims":[` +
	`{"path":"sections[0].blocks[0].text#0","at":"1. 이전 안내","text":"10월 20일 새 사무실로 이전합니다."},` +
	`{"path":"sections[0].blocks[0].text#1","at":"1. 이전 안내","text":"이전 기간에는 전화 응대가 어렵습니다."}]}`

type claimJudge struct {
	unsupportedText string
	kind            string
	kindOfText      map[string]string
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
		kind := firstNonEmptyString(judge.kind, "claim")
		probability := 0.05
		if shown.Claims[key].Text == judge.unsupportedText {
			probability = 0.9
		}
		if text, isNamed := judge.kindOfText[shown.Claims[key].Text]; isNamed {
			kind, probability = text, 0.9
		}
		answers[key] = model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: kind, Probabilities: map[string]float64{kind: probability, "source": 1 - probability}}
	}
	return model.DecisionResponse{Answers: answers}, nil
}

func (fixture taskFixture) installOfficeStandIn(t *testing.T) string {
	t.Helper()
	recordPath := filepath.Join(fixture.workspacePath, "office-calls.txt")
	entryPath := filepath.Join(BundledSkillRootPath(fixture.workspacePath), "office", "scripts", "office")
	writeTestFile(t, entryPath, "#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+shellSingleQuoted(recordPath)+"\n")
	if errorValue := os.Chmod(entryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	return recordPath
}

func (fixture taskFixture) deliverWithJudge(t *testing.T, judge *claimJudge, name string, otherJudge ...model.DecisionModel) map[string]json.RawMessage {
	t.Helper()
	if len(otherJudge) > 0 {
		fixture.builder.UseClaimDecisionModel(otherJudge[0])
	} else {
		fixture.builder.UseClaimDecisionModel(judge)
	}
	fixture.request.Prompt = "사무실 이전 안내문 만들어 줘. 10월 20일 새 사무실로 이전합니다."
	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/" + name})
	if result.Failed() || len(result.Attachments) != 1 {
		t.Fatalf("expected the file delivered, got %s", result.ContentText())
	}
	data := map[string]json.RawMessage{"content": json.RawMessage(MarshalBody(result.ContentText()))}
	_ = json.Unmarshal(result.Output.Data, &data)
	return data
}

func (fixture taskFixture) writeSnapshot(t *testing.T, name string, snapshot string) string {
	t.Helper()
	documentPath := fixture.writeDocument(t, name)
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
	fixture := newTaskFixture(t)
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
	fixture := newTaskFixture(t)
	fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	fixture.builder.UseClaimDecisionModel(&claimJudge{unsupportedText: "이전 기간에는 전화 응대가 어렵습니다."})
	fixture.request.Prompt = "사무실 이전 안내문 만들어 줘. 10월 20일 새 사무실로 이전합니다."

	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/notice.pdf"})

	expected := []string{`notice.pdf: left blank, for the reply to offer to complete: 1. 이전 안내 (it said "이전 기간에는 전화 응대가 어렵습니다.", which nothing the person gave supports)`}
	if strings.Join(result.ReplyNotes, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("expected the reply note %q, got %q", expected, result.ReplyNotes)
	}
	if !strings.Contains(result.ContentText(), expected[0]) {
		t.Fatalf("expected the model to read the same note, got %s", result.ContentText())
	}
}

func TestABlankAnEarlierRemakeLeftIsStillANoteForTheReply(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "q3-business-review.pptx", `{"command":"office create","deck":"/home/sample/documents/q3/slides.html","claims":[],`+
		`"slides":[{"slide":6,"layout":"cards","title":""}],"blanks":[{"field":"slides[5].units[0]","label":"슬라이드 6 제목"},{"field":"slides[5].units[3]","label":"슬라이드 6 본문"}]}`)
	fixture.builder.UseClaimDecisionModel(&claimJudge{})

	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/q3-business-review.pptx"})

	expected := "q3-business-review.pptx: left blank, for the reply to offer to complete: 슬라이드 6 제목, 슬라이드 6 본문"
	if strings.Join(result.ReplyNotes, "\n") != expected {
		t.Fatalf("expected the blanks the delivered deck holds as a note, got %q", result.ReplyNotes)
	}
}

func TestASupportedFileHasNoNoteForTheReply(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	fixture.builder.UseClaimDecisionModel(&claimJudge{})

	if result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/notice.pdf"}); len(result.ReplyNotes) != 0 {
		t.Fatalf("expected no note, got %q", result.ReplyNotes)
	}
}

func TestASupportedFileIsDeliveredAsItIs(t *testing.T) {
	fixture := newTaskFixture(t)
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
	fixture := newTaskFixture(t)
	fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)

	data := fixture.deliverWithJudge(t, &claimJudge{}, "notice.pdf")

	if !strings.Contains(string(data["claimChecks"]), `"asked":1`) {
		t.Fatalf("expected only the uncopied sentence asked, got %s", data["claimChecks"])
	}
}

func TestADeckIsRebuiltFromItsSourceWithTheBlank(t *testing.T) {
	fixture := newTaskFixture(t)
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
	fixture := newTaskFixture(t)
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
	fixture := newTaskFixture(t)
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
	fixture := newTaskFixture(t)
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

func TestAMistakeIsBlankedAndTheNoteAsksThePersonToConfirm(t *testing.T) {
	fixture := newTaskFixture(t)
	recordPath := fixture.installOfficeStandIn(t)
	documentPath := fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	fixture.builder.UseClaimDecisionModel(&claimJudge{unsupportedText: "10월 20일 새 사무실로 이전합니다.", kind: "mistake"})
	fixture.request.Prompt = "사무실 이전 안내문 만들어 줘. 10월 21일 새 사무실로 이전합니다."

	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/notice.pdf"})

	expected := []string{"merge", "letter", documentPath + officeContract.SourceSuffix, documentPath, "--blank", "sections[0].blocks[0].text#0"}
	if calls := officeCalls(t, recordPath); strings.Join(calls, " ") != strings.Join(expected, " ") {
		t.Fatalf("expected the remake %v, got %v", expected, calls)
	}
	note := `notice.pdf: left blank, for the reply to offer to complete: 1. 이전 안내 (it said "10월 20일 새 사무실로 이전합니다.", which differs from what the person gave: ask them to confirm the right value)`
	if strings.Join(result.ReplyNotes, "\n") != note {
		t.Fatalf("expected the reply note %q, got %q", note, result.ReplyNotes)
	}
}

func TestHollowIsRecordedAndNeverRewrittenOrBlanked(t *testing.T) {
	fixture := newTaskFixture(t)
	recordPath := fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)

	data := fixture.deliverWithJudge(t, &claimJudge{unsupportedText: "이전 기간에는 전화 응대가 어렵습니다.", kind: "hollow"}, "notice.pdf")

	if calls := officeCalls(t, recordPath); len(calls) != 0 {
		t.Fatalf("expected no remake for hollow, got %v", calls)
	}
	if !strings.Contains(string(data["claimChecks"]), `"hollow":[`) || !strings.Contains(string(data["claimChecks"]), `"outcome":"supported"`) {
		t.Fatalf("expected the hollow recorded and the file supported, got %s", data["claimChecks"])
	}
}

type derivationWriter struct{ wrongKey string }

func (writer derivationWriter) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (writer derivationWriter) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{Content: `{"checks":[{"key":"` + writer.wrongKey + `","working":"2+2=4","isWrong":true}]}`}, nil
}

func TestAWrongDerivationFoundByRecomputingIsBlanked(t *testing.T) {
	fixture := newTaskFixture(t)
	recordPath := fixture.installOfficeStandIn(t)
	documentPath := fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	fixture.builder.UseClaimRecompute(derivationWriter{wrongKey: "claim1"})

	fixture.deliverWithJudge(t, &claimJudge{kind: "derived"}, "notice.pdf")

	expected := []string{"merge", "letter", documentPath + officeContract.SourceSuffix, documentPath, "--blank", "sections[0].blocks[0].text#1"}
	if calls := officeCalls(t, recordPath); strings.Join(calls, " ") != strings.Join(expected, " ") {
		t.Fatalf("expected the remake %v, got %v", expected, calls)
	}
}

func (fixture taskFixture) installFailingOfficeStandIn(t *testing.T) {
	t.Helper()
	entryPath := filepath.Join(BundledSkillRootPath(fixture.workspacePath), "office", "scripts", "office")
	writeTestFile(t, entryPath, "#!/bin/sh\necho 'slide 2 needs a title' >&2\nexit 1\n")
	if errorValue := os.Chmod(entryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestAFlaggedValueTheRemakeCouldNotBlankIsANoteForTheReplyNamingIt(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.installFailingOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)

	data := fixture.deliverWithJudge(t, &claimJudge{unsupportedText: "이전 기간에는 전화 응대가 어렵습니다."}, "notice.pdf")

	if !strings.Contains(string(data["claimChecks"]), `"outcome":"remake_failed"`) {
		t.Fatalf("expected the failed remake recorded, got %s", data["claimChecks"])
	}
	if !strings.Contains(string(data["claimChecks"]), "slide 2 needs a title") {
		t.Fatalf("expected the remake's own message in the detail, got %s", data["claimChecks"])
	}
	content := string(data["content"])
	if !strings.Contains(content, "1. 이전 안내") || !strings.Contains(content, "이전 기간에는 전화 응대가 어렵습니다.") || !strings.Contains(content, "still in the file") {
		t.Fatalf("expected the reply note to name the unit left in the file, got %s", content)
	}
}

func TestEveryFlaggedValueLeftInTheFileIsNamedWhateverStoppedTheBlank(t *testing.T) {
	flagged := []officeclaimcheck.Verdict{{Claim: officeclaimcheck.Claim{Path: "slides[1].units[0]", At: "slide 2 title", Text: "2026 was a year of solid, profitable growth — now we choose the next frontier"}, Kind: "claim", Defect: "claim"}}
	for _, outcome := range []string{claimOutcomeRemakeFailed, claimOutcomeNoRemakeCommand, claimOutcomeUnreadSources} {
		notes := deliveredFileNotes("deck.pptx", nil, []officeClaimCheck{{File: "deck.pptx", Asked: 96, Flagged: flagged, Outcome: outcome, Detail: "exit status 1"}}, nil)
		if len(notes) != 1 || !strings.Contains(notes[0], "slide 2 title") || !strings.Contains(notes[0], "solid, profitable growth") || !strings.Contains(notes[0], "still in the file") {
			t.Fatalf("%s: expected a note naming the title left in the deck, got %q", outcome, notes)
		}
	}
}

type rewriteWriter struct {
	content string
	calls   int
}

func (writer *rewriteWriter) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (writer *rewriteWriter) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	writer.calls++
	return model.StructuredResponse{Content: writer.content}, nil
}

const hollowSentence = "이전 기간에는 전화 응대가 어렵습니다."

func (fixture taskFixture) deliverHollow(t *testing.T, writer *rewriteWriter, kindOfText map[string]string) []string {
	t.Helper()
	recordPath := fixture.installOfficeStandIn(t)
	fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	fixture.builder.UseClaimRewrite(writer)
	fixture.deliverWithJudge(t, &claimJudge{unsupportedText: hollowSentence, kind: "hollow", kindOfText: kindOfText}, "notice.pdf")
	return officeCalls(t, recordPath)
}

func TestAHollowSentenceIsReplacedByItsCleanRewriteWhenTheFileIsRemade(t *testing.T) {
	fixture := newTaskFixture(t)
	calls := fixture.deliverHollow(t, &rewriteWriter{content: `{"text":"이전 기간에는 전화가 연결되지 않습니다."}`}, map[string]string{"이전 기간에는 전화가 연결되지 않습니다.": "source"})
	if len(calls) < 2 || strings.Join(calls[len(calls)-2:], " ") != "--replace sections[0].blocks[0].text#1=이전 기간에는 전화가 연결되지 않습니다." {
		t.Fatalf("expected the remake to carry the replacement, got %v", calls)
	}
}

func TestAHollowSentenceWithAnEmptyRewriteIsBlankedBecauseASentenceMayGo(t *testing.T) {
	fixture := newTaskFixture(t)
	calls := fixture.deliverHollow(t, &rewriteWriter{content: `{"text":""}`}, nil)
	if len(calls) < 2 || strings.Join(calls[len(calls)-2:], " ") != "--blank sections[0].blocks[0].text#1" {
		t.Fatalf("expected the sentence blanked, got %v", calls)
	}
}

func TestAHollowSentenceStaysWhenItsRewriteIsUnreadable(t *testing.T) {
	fixture := newTaskFixture(t)
	if calls := fixture.deliverHollow(t, &rewriteWriter{content: "그냥 문장입니다."}, nil); len(calls) != 0 {
		t.Fatalf("expected no remake, got %v", calls)
	}
}

func TestAHollowSentenceStaysWhenItsRewriteIsJudgedWrong(t *testing.T) {
	fixture := newTaskFixture(t)
	if calls := fixture.deliverHollow(t, &rewriteWriter{content: `{"text":"전화는 3일간 불가합니다."}`}, map[string]string{"전화는 3일간 불가합니다.": "claim"}); len(calls) != 0 {
		t.Fatalf("expected no remake, got %v", calls)
	}
}

func TestOnlyASentenceOfAParagraphMayBeDroppedForBeingHollow(t *testing.T) {
	if isSentencePath("sections[0].title") || isSentencePath("slides[3].units[2]") {
		t.Fatalf("a title or a whole unit is a required slot")
	}
	if !isSentencePath("slides[3].units[2]#0") || !isSentencePath("sections[0].blocks[0].text#1") {
		t.Fatalf("a sentence of a paragraph ends in #<index>")
	}
}

const decisionsDeckSnapshot = `{"command":"office create","deck":"/home/sample/documents/decisions/slides.html","claims":[` +
	`{"path":"slides[0].units[0]","at":"slide 1","text":"Three decisions today"},` +
	`{"path":"slides[0].units[1]","at":"slide 1","text":"Approve the budget"},` +
	`{"path":"slides[0].units[2]","at":"slide 1","text":"Sign the lease"},` +
	`{"path":"slides[1].units[0]","at":"slide 2","text":"Thank you for your time"}]}`

const decisionsDeckAfterBlank = `{"command":"office create","deck":"/home/sample/documents/decisions/slides.html","claims":[` +
	`{"path":"slides[0].units[0]","at":"slide 1","text":"Three decisions today"},` +
	`{"path":"slides[0].units[1]","at":"slide 1","text":"Sign the lease"},` +
	`{"path":"slides[1].units[0]","at":"slide 2","text":"Thank you for your time"}]}`

type countingJudge struct {
	askedPerCall [][]string
	removedSeen  [][]string
}

func (judge *countingJudge) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	state, _ := json.Marshal(request.State)
	var shown struct {
		Claims  map[string]struct{ Text string } `json:"claims"`
		Removed []struct{ Text string }          `json:"removed"`
	}
	_ = json.Unmarshal(state, &shown)
	asked, removed := []string{}, []string{}
	for _, item := range shown.Removed {
		removed = append(removed, item.Text)
	}
	answers := map[string]model.DecisionAnswer{}
	for key := range request.Questions {
		text := shown.Claims[key].Text
		asked = append(asked, text)
		probability := 0.05
		if text == "Approve the budget" || (text == "Three decisions today" && len(removed) > 0) {
			probability = 0.9
		}
		answers[key] = model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: "claim", Probabilities: map[string]float64{"claim": probability}}
	}
	judge.askedPerCall = append(judge.askedPerCall, asked)
	judge.removedSeen = append(judge.removedSeen, removed)
	return model.DecisionResponse{Answers: answers}, nil
}

func (fixture taskFixture) installRewritingOfficeStandIn(t *testing.T, snapshotPath string, afterSnapshot string) string {
	t.Helper()
	recordPath := filepath.Join(fixture.workspacePath, "office-calls.txt")
	afterPath := filepath.Join(fixture.workspacePath, "after-snapshot.json")
	writeTestFile(t, afterPath, afterSnapshot)
	entryPath := filepath.Join(BundledSkillRootPath(fixture.workspacePath), "office", "scripts", "office")
	writeTestFile(t, entryPath, "#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+shellSingleQuoted(recordPath)+"\ncp "+shellSingleQuoted(afterPath)+" "+shellSingleQuoted(snapshotPath)+"\n")
	if errorValue := os.Chmod(entryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	return recordPath
}

func TestAStatementLeftBesideBlankedValuesIsJudgedAgainstWhatWasRemoved(t *testing.T) {
	fixture := newTaskFixture(t)
	documentPath := fixture.writeSnapshot(t, "decisions.pptx", decisionsDeckSnapshot)
	recordPath := fixture.installRewritingOfficeStandIn(t, documentPath+officeContract.SourceSuffix, decisionsDeckAfterBlank)
	judge := &countingJudge{}

	data := fixture.deliverWithJudge(t, nil, "decisions.pptx", judge)

	if len(judge.askedPerCall) < 2 || len(judge.askedPerCall[0]) != 4 || strings.Join(judge.askedPerCall[1], "|") != "Three decisions today|Sign the lease" && strings.Join(judge.askedPerCall[1], "|") != "Sign the lease|Three decisions today" {
		t.Fatalf("judged %v", judge.askedPerCall)
	}
	if strings.Join(judge.removedSeen[1], "|") != "Approve the budget" || strings.Join(judge.removedSeen[len(judge.removedSeen)-1], "|") != "Approve the budget|Three decisions today" || len(judge.removedSeen[0]) != 0 {
		t.Fatalf("removed shown as %v", judge.removedSeen)
	}
	calls := strings.Join(officeCalls(t, recordPath), " ")
	if !strings.Contains(calls, "--blank slides[0].units[1]") || !strings.Contains(calls, "--blank slides[0].units[0]") {
		t.Fatalf("remakes %s", calls)
	}
	if !strings.Contains(string(data["claimChecks"]), "slide 1") || !strings.Contains(string(data["claimChecks"]), `"outcome":"blanked"`) {
		t.Fatalf("check %s", data["claimChecks"])
	}
}
