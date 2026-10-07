package approvalgate

import (
	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
)

func ReplyOptionsOf(permissionOptions []acp.PermissionOption) []approvalreply.Option {
	offers := make([]approvalreply.Offer, 0, len(permissionOptions))
	for _, permissionOption := range permissionOptions {
		offers = append(offers, approvalreply.Offer{ID: string(permissionOption.OptionId), Name: permissionOption.Name, IsDeclining: isDeclining(permissionOption)})
	}
	return approvalreply.OptionsOf(offers)
}

func isDeclining(permissionOption acp.PermissionOption) bool {
	return permissionOption.Kind == acp.PermissionOptionKindRejectOnce || permissionOption.Kind == acp.PermissionOptionKindRejectAlways
}
