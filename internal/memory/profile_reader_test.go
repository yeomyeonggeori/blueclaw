package memory

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func TestProfileReaderResolverUsesCurrentDirectoryAccess(t *testing.T) {
	projection := policy.PolicyProjection{PersonAccessByPersonID: map[string]policy.PersonAccess{
		"reader": {PersonID: "reader", Circles: []string{"team"}, SecurityLevelRank: 2, GrantedClasses: []string{"restricted"}},
	}}
	directory := identity.NewIdentityService(projection)
	resolve := ProfileReaderResolver(directory)
	reader, isFound, errorValue := resolve(context.Background(), "reader")
	if errorValue != nil || !isFound || reader.PersonID != "reader" || reader.SecurityLevelRank != 2 || len(reader.GrantedClasses) != 1 {
		t.Fatalf("expected the directory's current reader: %+v (%v)", reader, errorValue)
	}
	directory.ReloadPolicyProjection(policy.PolicyProjection{})
	_, isFound, errorValue = resolve(context.Background(), "reader")
	if errorValue != nil || isFound {
		t.Fatalf("deleted policy person must no longer resolve: found=%v (%v)", isFound, errorValue)
	}
}
