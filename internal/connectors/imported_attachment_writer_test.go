package connectors

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func attachmentWriterForTest(t *testing.T, personID string) (importedAttachmentWriter, string) {
	t.Helper()
	workspacePath := t.TempDir()
	terminalService := security.NewShellService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     30,
	})
	writer := importedAttachmentWriter{
		workspaceActorFactory: security.NewDirectWorkspaceActorFactory(terminalService),
		personID:              personID,
	}
	return writer, workspacePath
}

// The bridge that fetched this file runs beside the workspace, not inside it,
// and used to write its own copy where the agent's /workspace never looks. The
// agent was then handed a path that stat could not find.
func TestAnAttachmentLandsWhereTheAgentWasToldItIs(t *testing.T) {
	writer, workspacePath := attachmentWriterForTest(t, "person-1")
	agentPath := filepath.Join(workspacePath, "private/people/person-1/inbox/buzz/dm/map.png")

	written := writer.writeAll(context.Background(), []InputAttachment{{
		Platform:      "buzz",
		Filename:      "map.png",
		Path:          agentPath,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("image bytes")),
	}})

	if len(written) != 1 || !written[0].IsAvailable {
		t.Fatalf("expected the attachment to be available, got %+v", written)
	}
	if written[0].Path != agentPath {
		t.Fatalf("expected the path the agent was told about, got %q", written[0].Path)
	}
	content, errorValue := os.ReadFile(agentPath)
	if errorValue != nil {
		t.Fatalf("expected the file where the agent will look: %v", errorValue)
	}
	if string(content) != "image bytes" {
		t.Fatalf("expected the fetched bytes, got %q", content)
	}
}

func TestTheContentIsNotCarriedAnyFurtherThanTheWrite(t *testing.T) {
	writer, workspacePath := attachmentWriterForTest(t, "person-1")

	written := writer.writeAll(context.Background(), []InputAttachment{{
		Path:          filepath.Join(workspacePath, "private/people/person-1/inbox/buzz/dm/map.png"),
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("image bytes")),
	}})

	if written[0].ContentBase64 != "" {
		t.Fatal("expected the bytes to stop at the file rather than reach the ledger")
	}
}

// The same picture arriving twice keeps the name it already has; a different
// file under a taken name gets its own.
func TestTheSameFileTwiceIsOneFile(t *testing.T) {
	writer, workspacePath := attachmentWriterForTest(t, "person-1")
	agentPath := filepath.Join(workspacePath, "private/people/person-1/inbox/buzz/dm/map.png")
	sameFile := InputAttachment{Path: agentPath, ContentBase64: base64.StdEncoding.EncodeToString([]byte("image bytes"))}

	writer.writeAll(context.Background(), []InputAttachment{sameFile})
	written := writer.writeAll(context.Background(), []InputAttachment{sameFile})

	if written[0].Path != agentPath {
		t.Fatalf("expected the same file to keep its name, got %q", written[0].Path)
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "private/people/person-1/inbox/buzz/dm/map-2.png")); errorValue == nil {
		t.Fatal("expected no second copy of the same file")
	}
}

func TestADifferentFileUnderATakenNameGetsItsOwn(t *testing.T) {
	writer, workspacePath := attachmentWriterForTest(t, "person-1")
	agentPath := filepath.Join(workspacePath, "private/people/person-1/inbox/buzz/dm/map.png")

	writer.writeAll(context.Background(), []InputAttachment{{Path: agentPath, ContentBase64: base64.StdEncoding.EncodeToString([]byte("first"))}})
	written := writer.writeAll(context.Background(), []InputAttachment{{Path: agentPath, ContentBase64: base64.StdEncoding.EncodeToString([]byte("second"))}})

	if written[0].Path != filepath.Join(workspacePath, "private/people/person-1/inbox/buzz/dm/map-2.png") {
		t.Fatalf("expected a name of its own, got %q", written[0].Path)
	}
	content, errorValue := os.ReadFile(written[0].Path)
	if errorValue != nil || string(content) != "second" {
		t.Fatalf("expected the second file's own bytes, got %q (%v)", content, errorValue)
	}
}

func TestAnAttachmentNobodyCanBeWrittenAsSaysSo(t *testing.T) {
	writer := importedAttachmentWriter{}

	written := writer.writeAll(context.Background(), []InputAttachment{{
		Path:          "/workspace/private/people/person-1/inbox/buzz/dm/map.png",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("image bytes")),
	}})

	if written[0].IsAvailable || written[0].ErrorCode != connectorAttachmentImportRefusedCode {
		t.Fatalf("expected the attachment to arrive refused, got %+v", written[0])
	}
	if written[0].Path != "" {
		t.Fatal("expected no path to a file that was never written")
	}
}

// An attachment whose import was refused has no path and, on buzz, no fileID.
// The catalog used to drop it for that, so the model was left with nothing but
// the url in the message text and invented a workspace path from it.
func TestARefusedAttachmentStaysInTheCatalog(t *testing.T) {
	refused := InputAttachment{
		Platform:  "buzz",
		URL:       "https://relay.example.test/media/abc.png",
		Filename:  "image",
		ErrorCode: connectorAttachmentImportRefusedCode,
		Message:   "no workspace identity to write an attachment as",
	}

	materials := agentVisibleContextMaterials([]InputAttachment{refused})

	if len(materials) != 1 {
		t.Fatalf("expected the refused attachment to stay visible, got %+v", materials)
	}
	if materials[0].IsAvailable || materials[0].ErrorCode == "" {
		t.Fatalf("expected the refusal to travel with it, got %+v", materials[0])
	}
}
