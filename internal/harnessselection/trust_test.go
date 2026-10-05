package harnessselection

import (
	"reflect"
	"testing"
)

func TestTheExternalAgentDefinitionDeclaresTrustOfExactlyBlueclawsServer(t *testing.T) {
	expected := map[string]any{"claudeCode": map[string]any{"options": map[string]any{"allowedTools": []string{"mcp__blueclaw"}}}}

	if !reflect.DeepEqual(externalAgentToolCatalogTrust.SessionMeta, expected) {
		t.Fatalf("got %+v", externalAgentToolCatalogTrust.SessionMeta)
	}
}
