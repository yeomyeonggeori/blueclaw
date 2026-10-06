package modelstandin

import (
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func ScriptedOptionID(options []approvalreply.Option, scriptedChoices []string, scriptedApproval *agentcontract.ApprovalSignal) (string, bool) {
	for _, option := range options {
		if isScriptedOption(option, scriptedChoices, scriptedApproval) {
			return option.ID, true
		}
	}
	return "", false
}

func isScriptedOption(option approvalreply.Option, scriptedChoices []string, scriptedApproval *agentcontract.ApprovalSignal) bool {
	if len(scriptedChoices) > 0 {
		return option.ID == scriptedChoices[0] || strings.HasSuffix(option.ID, ":"+scriptedChoices[0])
	}
	if scriptedApproval == nil {
		return false
	}
	isRejecting := option.Meaning == approvalreply.RejectMeaning
	return isRejecting == (*scriptedApproval == agentcontract.ApprovalSignalReject)
}
