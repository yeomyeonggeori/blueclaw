package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (fixture officeContextFixture) writeDocument(t *testing.T, name string) string {
	t.Helper()
	documentPath := filepath.Join(fixture.homePath(), "documents", name)
	if errorValue := os.MkdirAll(filepath.Dir(documentPath), 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, documentPath, "document-bytes")
	return documentPath
}

func (fixture officeContextFixture) deliver(t *testing.T, path string) (bool, string) {
	t.Helper()
	result := fixture.invoke(t, "file_deliver", map[string]string{"path": path})
	return !result.Failed() && len(result.Attachments) == 1, result.ContentText()
}

func TestADocumentThisTaskWroteWithoutASkillIsDeliveredLikeAnyFile(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	fixture.writeDocument(t, "native_install_rig_final.pdf")

	isDelivered, content := fixture.deliver(t, "documents/native_install_rig_final.pdf")

	if !isDelivered {
		t.Fatalf("a PDF a script wrote in this task was refused: %s", content)
	}
	if strings.Contains(content, "office") {
		t.Fatalf("delivering a file names a skill the task did not use: %s", content)
	}
}
