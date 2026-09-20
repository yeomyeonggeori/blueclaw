package agentruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestSiteToolNeedsSourceBundleOnlyForServe(t *testing.T) {
	if !siteToolNeedsSourceBundle("site_serve") || !siteToolNeedsSourceBundle(" site_serve ") {
		t.Fatal("expected site_serve to require a source bundle")
	}
	for _, toolName := range []string{"site_list", "site_unserve", "write", ""} {
		if siteToolNeedsSourceBundle(toolName) {
			t.Fatalf("expected %q not to require a source bundle", toolName)
		}
	}
}

func TestPrepareSiteSourceBundleRequiresSourceWorkspacePath(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	for _, toolInput := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"title":"Demo","mode":"publish"}`),
		json.RawMessage(`{"sourceWorkspacePath":"   "}`),
	} {
		_, toolFailure, errorValue := toolCatalogBuilder.prepareSiteSourceBundle(context.Background(), ToolCatalogRequest{}, toolInput)
		if toolFailure != nil {
			t.Fatalf("expected input validation error, got tool failure %+v", toolFailure)
		}
		if errorValue == nil || !strings.Contains(errorValue.Error(), "sourceWorkspacePath is required") {
			t.Fatalf("expected sourceWorkspacePath requirement for %s, got %v", toolInput, errorValue)
		}
	}
}

func TestSiteSourceBundleSHA256IdentifiesDecodedBundle(t *testing.T) {
	contentBase64 := base64.StdEncoding.EncodeToString([]byte("site source bundle"))
	sourceSHA256, errorValue := siteSourceBundleSHA256(contentBase64)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if sourceSHA256 != "254cc09182b94752e96474af9ba307f74dcfff4e8dfa5b0c4a76f97e634c1c28" {
		t.Fatalf("unexpected source SHA-256: %s", sourceSHA256)
	}
	if _, errorValue := siteSourceBundleSHA256("not-base64"); errorValue == nil {
		t.Fatal("expected invalid base64 rejection")
	}
}
