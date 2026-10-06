package architecture

import (
	"strings"
	"testing"
)

func TestTheApprovalBoundaryCallsNoLanguageModelItself(t *testing.T) {
	for _, listed := range listProductionPackages(t) {
		if !strings.HasSuffix(listed.ImportPath, "/internal/approvalgate") {
			continue
		}
		for _, importPath := range listed.Imports {
			if importPath == bluecollarModulePath+"model" {
				t.Errorf("%s imports %s; the approval question is worded by bluecollar's approval.Worder, handed over as a holdrecord.QuestionWorder (#537)", listed.ImportPath, importPath)
			}
		}
	}
}
