package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const quoteSource = `{"schema":"kr/quote","given":{"recipient":"견본상사"},"known":{"issueDate":"2026-10-04"},"claims":[{"path":"recipient","at":"수신처","text":"견본상사"},{"path":"deliveryPlace","at":"납품장소","text":"견본상사 본사"}]}`

func deliverQuote(t *testing.T, workspacePath string) toolcontract.ToolResult {
	t.Helper()
	toolRegistry := newFileToolTestCatalogBuilder(workspacePath).BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
	})
	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileDeliverToolName,
		Input:    toolcontract.MarshalToolInput(map[string]string{"path": "documents/quote.pdf"}),
	})
	if errorValue != nil || result.Failed() || len(result.Attachments) != 1 {
		t.Fatalf("expected the quote delivered, got %+v (%v)", result, errorValue)
	}
	return result
}

func quoteDirectory(t *testing.T, workspacePath string) string {
	t.Helper()
	directoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "documents")
	if errorValue := os.MkdirAll(directoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(directoryPath, "quote.pdf"), "%PDF-1.7")
	return directoryPath
}

func TestADeliveredFileCarriesTheClaimsItsSourceLists(t *testing.T) {
	workspacePath := t.TempDir()
	writeTestFile(t, filepath.Join(quoteDirectory(t, workspacePath), "quote.pdf"+toolcontract.DeliveredSourceSuffix), quoteSource)

	source := deliverQuote(t, workspacePath).Attachments[0].Source

	if source == nil || !source.IsLayoutOwnedByCode || len(source.Claims) != 2 || source.Claims[1].Text != "견본상사 본사" {
		t.Fatalf("expected the sidecar's claims on the attachment, got %+v", source)
	}
}

func TestASourceThePersonCannotReadIsNotHandedOver(t *testing.T) {
	workspacePath := t.TempDir()
	sourcePath := filepath.Join(quoteDirectory(t, workspacePath), "quote.pdf"+toolcontract.DeliveredSourceSuffix)
	writeTestFile(t, sourcePath, quoteSource)
	if errorValue := os.Chmod(sourcePath, 0000); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.Chmod(sourcePath, 0600) })

	if source := deliverQuote(t, workspacePath).Attachments[0].Source; source != nil {
		t.Fatalf("expected the read to go through the person's file permissions and fail, got %+v", source)
	}
}

func TestAFileWithoutASourceIsDeliveredWithoutClaims(t *testing.T) {
	workspacePath := t.TempDir()
	quoteDirectory(t, workspacePath)

	if source := deliverQuote(t, workspacePath).Attachments[0].Source; source != nil {
		t.Fatalf("expected no source, got %+v", source)
	}
}
