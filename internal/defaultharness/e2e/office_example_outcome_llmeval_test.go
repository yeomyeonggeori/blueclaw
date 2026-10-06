//go:build appliance && llmeval && !nobundledharness

package e2e

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func officeExampleResult(status task.TaskStatus, filenames ...string) VirtualSessionResult {
	attachments := make([]toolcontract.FileAttachment, 0, len(filenames))
	for _, filename := range filenames {
		attachments = append(attachments, toolcontract.FileAttachment{Filename: filename})
	}
	return VirtualSessionResult{TurnResults: []VirtualTurnResult{{TaskStatus: status, FailureReason: "max_tool_calls", Attachments: attachments}}}
}

func TestOfficeExampleShortfallRefusesATaskThatDidNotDeliverTheFile(t *testing.T) {
	request := officeExampleRequest{Name: "deck", Deliverable: "pptx"}
	cases := map[string]VirtualSessionResult{
		"blocked without a file":        officeExampleResult(task.TaskStatusBlocked),
		"blocked with the file":         officeExampleResult(task.TaskStatusBlocked, "deck.pptx"),
		"completed with another format": officeExampleResult(task.TaskStatusCompleted, "deck.pdf"),
		"asking instead of delivering":  officeExampleResult(task.TaskStatusWaitingUserInput),
		"no turn at all":                {},
	}
	for name, result := range cases {
		if shortfall := officeExampleShortfall(request, result); shortfall == "" {
			t.Errorf("%s: passed", name)
		}
	}
}

func TestOfficeExampleShortfallNamesTheStatusAndReason(t *testing.T) {
	shortfall := officeExampleShortfall(officeExampleRequest{Name: "deck", Deliverable: "pptx"}, officeExampleResult(task.TaskStatusBlocked))
	if !strings.Contains(shortfall, "blocked") || !strings.Contains(shortfall, "max_tool_calls") || !strings.Contains(shortfall, ".pptx") {
		t.Fatalf("shortfall %q does not name the status, the reason and the file it expected", shortfall)
	}
}

func TestOfficeExampleShortfallAcceptsACompletedTaskThatDeliveredTheFile(t *testing.T) {
	request := officeExampleRequest{Name: "deck", Deliverable: ".PPTX"}
	if shortfall := officeExampleShortfall(request, officeExampleResult(task.TaskStatusCompleted, "notes.md", "지원사업-발표.pptx")); shortfall != "" {
		t.Fatalf("a delivered deck was refused: %s", shortfall)
	}
}
