//go:build !nobundledharness

package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
)

const workbookSnapshot = `{"declaration":"/home/sample/documents/sales.workbook.json","compiled":[{"sheet":"Data","range":"A1:C4"}],"blanks":[],` +
	`"claims":[{"path":"title","at":"title","text":"Quarterly sales"}],` +
	`"tables":[{"name":"Data","columns":["Quarter","Region","Revenue"],"rowCount":3}],` +
	`"views":[{"title":"By quarter","sheet":"Summary","rows":[["Quarter","Revenue"],["Q1","1,230"]]}],` +
	`"charts":[{"view":"By quarter","type":"line","title":"Revenue trend"}]}`

func (fixture taskFixture) deliveredHolds(t *testing.T, name string) string {
	t.Helper()
	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/" + name})
	if result.Failed() || len(result.Attachments) != 1 {
		t.Fatalf("expected the file delivered, got %s", result.ContentText())
	}
	return string(result.Attachments[0].Holds)
}

func TestADeliveredFileCarriesWhatItsSnapshotSaysItHolds(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.writeSnapshot(t, "sales.xlsx", workbookSnapshot)

	holds := fixture.deliveredHolds(t, "sales.xlsx")

	expected := `{"blanks":[],"charts":[{"view":"By quarter","type":"line","title":"Revenue trend"}],` +
		`"tables":[{"name":"Data","columns":["Quarter","Region","Revenue"],"rowCount":3}],` +
		`"views":[{"title":"By quarter","sheet":"Summary","rows":[["Quarter","Revenue"],["Q1","1,230"]]}]}`
	if holds != expected {
		t.Fatalf("expected only the content fields the contract names, got %s", holds)
	}
}

func TestHoldsAreReadAfterTheClaimRemakeRewroteTheSnapshot(t *testing.T) {
	fixture := newTaskFixture(t)
	documentPath := fixture.writeSnapshot(t, "notice.pdf", noticeSnapshot)
	entryPath := filepath.Join(BundledSkillRootPath(fixture.workspacePath), "office", "scripts", "office")
	remade := `{"schema":"letter","given":{"title":null},"blanks":[{"field":"sections[0].blocks[0].text#1","label":"1. 이전 안내"}]}`
	writeTestFile(t, entryPath, "#!/bin/sh\nprintf '%s' "+shellSingleQuoted(remade)+" > "+shellSingleQuoted(documentPath+officeContract.SourceSuffix)+"\n")
	if errorValue := os.Chmod(entryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	fixture.builder.UseClaimDecisionModel(&claimJudge{unsupportedText: "이전 기간에는 전화 응대가 어렵습니다."})
	fixture.request.Prompt = "사무실 이전 안내문 만들어 줘. 10월 20일 새 사무실로 이전합니다."

	holds := fixture.deliveredHolds(t, "notice.pdf")

	if holds != `{"blanks":[{"field":"sections[0].blocks[0].text#1","label":"1. 이전 안내"}],"given":{"title":null},"schema":"letter"}` {
		t.Fatalf("expected the holds of the remade file, got %s", holds)
	}
}

const showcaseDeckSnapshotWithoutSlides = `{"command":"office create","deck":"/home/sample/documents/q3-review/slides.html",` +
	`"claims":[{"path":"slides[0].units[0]","at":"슬라이드 1 제목","text":"3분기 업무 리뷰"}],"blanks":[]}`

const deckSnapshot = `{"command":"office create","deck":"/home/sample/documents/q3-review/slides.html",` +
	`"claims":[{"path":"slides[0].units[0]","at":"슬라이드 1 제목","text":"3분기 업무 리뷰"}],` +
	`"slides":[{"slide":1,"layout":"cover","title":"3분기 업무 리뷰","text":["발표 이샘플"]},` +
	`{"slide":2,"layout":"chart","title":"매출 달성률 90%","charts":[{"type":"column","labels":["목표","실적"],"values":["12","10.8"],"unit":"억 원"}],"icons":["target"]}],` +
	`"blanks":[]}`

func TestADeckSnapshotHoldingOnlyItsBlanksCarriesNoHolds(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.writeSnapshot(t, "q3-review.pptx", showcaseDeckSnapshotWithoutSlides)

	if holds := fixture.deliveredHolds(t, "q3-review.pptx"); holds != "" {
		t.Fatalf("expected a snapshot that says nothing of what the deck holds to give no holds, got %s", holds)
	}
}

func TestADeckCarriesItsSlidesAndBlanks(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.writeSnapshot(t, "q3-review.pptx", deckSnapshot)

	holds := fixture.deliveredHolds(t, "q3-review.pptx")

	expected := `{"blanks":[],"slides":[{"slide":1,"layout":"cover","title":"3분기 업무 리뷰","text":["발표 이샘플"]},` +
		`{"slide":2,"layout":"chart","title":"매출 달성률 90%","charts":[{"type":"column","labels":["목표","실적"],"values":["12","10.8"],"unit":"억 원"}],"icons":["target"]}]}`
	if holds != expected {
		t.Fatalf("expected the deck's slides and blanks, got %s", holds)
	}
}

func TestAFileWithoutASnapshotCarriesNoHolds(t *testing.T) {
	fixture := newTaskFixture(t)
	documentPath := filepath.Join(fixture.homePath(), "documents", "notes.txt")
	writeTestFile(t, documentPath, "notes")

	if holds := fixture.deliveredHolds(t, "notes.txt"); holds != "" {
		t.Fatalf("expected no holds, got %s", holds)
	}
}

func TestHoldsFollowThePersonsFilePermissions(t *testing.T) {
	fixture := newTaskFixture(t)
	documentPath := fixture.writeSnapshot(t, "sales.xlsx", workbookSnapshot)
	snapshotPath := documentPath + officeContract.SourceSuffix
	if errorValue := os.Chmod(snapshotPath, 0o000); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.Chmod(snapshotPath, 0o600) })

	if holds := fixture.deliveredHolds(t, "sales.xlsx"); holds != "" {
		t.Fatalf("expected a snapshot the person cannot read to give no holds, got %s", holds)
	}
}
