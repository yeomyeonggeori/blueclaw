package approvalgate

import (
	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
)

func ReplyOptionsOf(permissionOptions []acp.PermissionOption) []approvalreply.Option {
	options := make([]approvalreply.Option, 0, len(permissionOptions))
	for _, permissionOption := range permissionOptions {
		options = append(options, approvalreply.Option{ID: string(permissionOption.OptionId), Meaning: replyMeaningOf(permissionOption)})
	}
	return options
}

func replyMeaningOf(permissionOption acp.PermissionOption) string {
	switch permissionOption.Kind {
	case acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways:
		return approvalreply.RejectMeaning
	}
	return approvalreply.AllowMeaning(permissionOption.Name)
}
