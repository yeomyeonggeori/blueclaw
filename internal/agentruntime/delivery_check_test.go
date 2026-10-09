//go:build !nobundledharness

package agentruntime

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const checkingSkill = `---
name: checker
description: checks what a task delivers
metadata:
  kim.intern.delivery-check: "check.sh --quiet"
---

# Checker
`

func (fixture taskFixture) installCheckingSkill(t *testing.T, script string) {
	t.Helper()
	directory := filepath.Join(fixture.workspacePath, "skills", "checker")
	writeTestFile(t, filepath.Join(directory, "SKILL.md"), checkingSkill)
	writeTestFile(t, filepath.Join(directory, "check.sh"), "#!/bin/sh\n"+script)
	if errorValue := os.Chmod(filepath.Join(directory, "check.sh"), 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func deliveredText(t *testing.T, content string) string {
	t.Helper()
	decoded, errorValue := base64.StdEncoding.DecodeString(content)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(decoded)
}

func TestASkillsDeliveryCheckRunsOnTheFileBeforeItIsDeliveredWithTheTaskAndTheHost(t *testing.T) {
	fixture := newScriptHostFixture(t)
	fixture.installCheckingSkill(t, `printf 'checked' > "$2"
printf '{"notes":["%s: context %s, host %s"]}' "$(basename "$2")" "$([ -f "$SKILL_TASK_CONTEXT" ] && echo yes)" "$([ -n "$SKILL_HOST_URL" ] && echo yes)" > "$2.meta.json"
`)
	fixture.writeDocument(t, "memo.docx")

	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/memo.docx"})

	if result.Failed() || len(result.Attachments) != 1 {
		t.Fatalf("expected the file delivered, got %s", result.ContentText())
	}
	if text := deliveredText(t, result.Attachments[0].ContentBase64); text != "checked" {
		t.Fatalf("the file went out as the check found it, not as the check left it: %q", text)
	}
	if !slices.Contains(result.ReplyNotes, "memo.docx: context yes, host yes") {
		t.Fatalf("the reply notes were %q", result.ReplyNotes)
	}
}

func TestASkillsDeliveryCheckThatRefusesAFileStopsTheWholeDeliveryAndTellsTheModelWhy(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.installCheckingSkill(t, `case "$2" in
*deck.pptx) printf '{"notes":[],"refusal":"deck.pptx was not delivered: rewrite slide 6 without its unsupported row"}' > "$2.meta.json" ;;
*) printf '{"notes":["%s: fine"]}' "$(basename "$2")" > "$2.meta.json" ;;
esac
`)
	fixture.writeDocument(t, "memo.docx")
	fixture.writeDocument(t, "deck.pptx")

	result := fixture.invoke(t, "file_deliver", map[string]any{"files": []map[string]string{{"path": "documents/memo.docx"}, {"path": "documents/deck.pptx"}}})

	if !result.Failed() || len(result.Attachments) != 0 {
		t.Fatalf("a refused file went out, or took the files beside it along: %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), "rewrite slide 6 without its unsupported row") {
		t.Fatalf("the model was not told why the delivery was refused: %s", result.ContentText())
	}
	if !result.Failure.Retryable {
		t.Fatal("a refused delivery must be retryable once the file is rewritten")
	}
}

func TestAFailedDeliveryCheckStillDeliversAndTellsTheReplyWhy(t *testing.T) {
	fixture := newTaskFixture(t)
	fixture.installCheckingSkill(t, "echo 'the judge did not answer' >&2\nexit 3\n")
	fixture.writeDocument(t, "memo.docx")

	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/memo.docx"})

	if result.Failed() || len(result.Attachments) != 1 {
		t.Fatalf("a failed check stopped the delivery: %s", result.ContentText())
	}
	if !slices.ContainsFunc(result.ReplyNotes, func(note string) bool {
		return strings.Contains(note, "checker") && strings.Contains(note, "the judge did not answer")
	}) {
		t.Fatalf("the reply was not told the check failed: %q", result.ReplyNotes)
	}
}
