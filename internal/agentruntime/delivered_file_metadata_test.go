//go:build !nobundledharness

package agentruntime

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

const workbookMetadata = `{"holds":{"tables":[{"name":"Data","columns":["Quarter","Revenue"],"rowCount":3}]},` +
	`"notes":["sales.xlsx: left blank, for the reply to offer to complete: Q4 revenue"]}`

func (fixture taskFixture) writeDocumentWithMetadata(t *testing.T, name string, metadata string) string {
	t.Helper()
	documentPath := fixture.writeDocument(t, name)
	writeTestFile(t, documentPath+deliveredFileMetadataSuffix, metadata)
	return documentPath
}

func TestADeliveredFileCarriesWhatItsMetadataSaysItHoldsAndItsNotesReachTheReply(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.writeDocumentWithMetadata(t, "sales.xlsx", workbookMetadata)

	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/sales.xlsx"})

	if result.Failed() || len(result.Attachments) != 1 {
		t.Fatalf("expected the file delivered, got %s", result.ContentText())
	}
	if holds := string(result.Attachments[0].Holds); holds != `{"tables":[{"name":"Data","columns":["Quarter","Revenue"],"rowCount":3}]}` {
		t.Fatalf("the attachment holds %s", holds)
	}
	if !slices.Contains(result.ReplyNotes, "sales.xlsx: left blank, for the reply to offer to complete: Q4 revenue") {
		t.Fatalf("the reply notes were %q", result.ReplyNotes)
	}
}

func TestMetadataOlderThanItsFileSaysNothingAboutIt(t *testing.T) {
	fixture := newTaskFixture(t)
	documentPath := fixture.writeDocumentWithMetadata(t, "sales.xlsx", workbookMetadata)
	earlier := time.Now().Add(-time.Hour)
	if errorValue := os.Chtimes(documentPath+deliveredFileMetadataSuffix, earlier, earlier); errorValue != nil {
		t.Fatal(errorValue)
	}

	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/sales.xlsx"})

	if len(result.Attachments) != 1 || len(result.Attachments[0].Holds) != 0 || len(result.ReplyNotes) != 0 {
		t.Fatalf("stale metadata reached the delivery: holds %s, notes %q", result.Attachments[0].Holds, result.ReplyNotes)
	}
}

func TestAFileWithoutMetadataIsDeliveredWithNoHoldsOrNotes(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.writeDocument(t, filepath.Base("plain.pdf"))

	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/plain.pdf"})

	if len(result.Attachments) != 1 || len(result.Attachments[0].Holds) != 0 || len(result.ReplyNotes) != 0 {
		t.Fatalf("a file with no metadata carried holds %s and notes %q", result.Attachments[0].Holds, result.ReplyNotes)
	}
}
