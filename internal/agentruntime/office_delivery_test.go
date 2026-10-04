package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func (fixture officeContextFixture) writeDocument(t *testing.T, name string, withSource bool) string {
	t.Helper()
	documentPath := filepath.Join(fixture.homePath(), "documents", name)
	if errorValue := os.MkdirAll(filepath.Dir(documentPath), 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, documentPath, "document-bytes")
	if withSource {
		writeTestFile(t, documentPath+officeContract.SourceSuffix, `{"command":"office create"}`)
	}
	return documentPath
}

func (fixture officeContextFixture) deliver(t *testing.T, path string) (bool, string) {
	t.Helper()
	result := fixture.invoke(t, "file_deliver", map[string]string{"path": path})
	return !result.Failed() && len(result.Attachments) == 1, result.ContentText()
}

func TestANewOfficeFileWithoutItsSourceIsNotDeliveredAndTheRefusalSaysHowToMakeIt(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	fixture.writeDocument(t, "workbook.xlsx", false)

	isDelivered, content := fixture.deliver(t, "documents/workbook.xlsx")

	if isDelivered {
		t.Fatal("a workbook a script wrote in this task was delivered")
	}
	for _, expected := range []string{"workbook.xlsx" + officeContract.SourceSuffix, "merge or create", "from its own path"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("the refusal does not say %q: %s", expected, content)
		}
	}
}

func TestANewOfficeFileAnOfficeCommandWroteIsDelivered(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	fixture.writeDocument(t, "quote.pdf", true)

	if isDelivered, content := fixture.deliver(t, "documents/quote.pdf"); !isDelivered {
		t.Fatalf("a file merge wrote was refused: %s", content)
	}
}

func TestAnOfficeFileFromBeforeTheTaskIsDeliveredWithoutASource(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	documentPath := fixture.writeDocument(t, "last-quarter.docx", false)
	earlier := fixture.taskRun.CreatedAt.Add(-time.Hour)
	if errorValue := os.Chtimes(documentPath, earlier, earlier); errorValue != nil {
		t.Fatal(errorValue)
	}

	if isDelivered, content := fixture.deliver(t, "documents/last-quarter.docx"); !isDelivered {
		t.Fatalf("an earlier task's file was refused: %s", content)
	}
}

func TestAnOfficeFileThePersonAttachedIsDeliveredWithoutASource(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	attachmentPath := filepath.Join(fixture.homePath(), "inbox", "contract.pdf")
	if errorValue := os.MkdirAll(filepath.Dir(attachmentPath), 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, attachmentPath, "pdf-bytes")
	fixture.request.VisibleContext = agentcontract.VisibleContext{CurrentMaterials: []agentcontract.VisibleContextMaterial{{MaterialID: "m-1", Filename: "contract.pdf", Path: attachmentPath, IsAvailable: true}}}

	if isDelivered, content := fixture.deliver(t, attachmentPath); !isDelivered {
		t.Fatalf("the person's own file was refused: %s", content)
	}
}

func TestAFileThatIsNotAnOfficeDocumentNeedsNoSource(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	fixture.writeDocument(t, "chart.png", false)

	if isDelivered, content := fixture.deliver(t, "documents/chart.png"); !isDelivered {
		t.Fatalf("an image was refused: %s", content)
	}
}
