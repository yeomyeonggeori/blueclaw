package architecture

import (
	"strings"
	"testing"
)

func TestTheApprovalBoundaryCallsNoLanguageModelItself(t *testing.T) {
	for _, file := range listSourceFiles(t) {
		if file.isTest || !strings.HasPrefix(file.path, "internal/approvalgate/") {
			continue
		}
		if file.importsModel {
			t.Errorf("%s imports the language model port; the approval question is worded by the QuestionWorder the host hands over (#537)", file.path)
		}
	}
}
