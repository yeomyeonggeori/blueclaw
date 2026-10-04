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

func (fixture officeContextFixture) deliveredHolds(t *testing.T, name string) string {
	t.Helper()
	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/" + name})
	if result.Failed() || len(result.Attachments) != 1 {
		t.Fatalf("expected the file delivered, got %s", result.ContentText())
	}
	return string(result.Attachments[0].Holds)
}

func TestADeliveredFileCarriesWhatItsSnapshotSaysItHolds(t *testing.T) {
	fixture := newOfficeContextFixture(t)
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
	fixture := newOfficeContextFixture(t)
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

func TestAFileWithoutASnapshotCarriesNoHolds(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	documentPath := filepath.Join(fixture.homePath(), "documents", "notes.txt")
	writeTestFile(t, documentPath, "notes")

	if holds := fixture.deliveredHolds(t, "notes.txt"); holds != "" {
		t.Fatalf("expected no holds, got %s", holds)
	}
}

func TestHoldsFollowThePersonsFilePermissions(t *testing.T) {
	fixture := newOfficeContextFixture(t)
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
