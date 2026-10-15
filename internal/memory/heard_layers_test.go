package memory_test

import (
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func companyOfFour() []policy.PersonAccess {
	return []policy.PersonAccess{
		{PersonID: "person-chief", Circles: []string{"member", "leadership", "admin"}},
		{PersonID: "person-director", Circles: []string{"member", "leadership"}},
		{PersonID: "person-engineer", Circles: []string{"member", "platform"}},
		{PersonID: "person-designer", Circles: []string{"member", "platform"}},
	}
}

func TestARoomOfExactlyOneCircleKeepsWhatItHearsInThatCircle(t *testing.T) {
	layers := memory.LayersReadByExactly([]string{"person-chief", "person-director"}, companyOfFour(), nil)
	if !slices.Equal(layers, []memory.Scope{memory.CircleScope("leadership")}) {
		t.Fatalf("expected the leadership circle alone, got %+v", layers)
	}
}

func TestSomebodyOutsideTheCircleClosesItsLayer(t *testing.T) {
	layers := memory.LayersReadByExactly([]string{"person-chief", "person-director", "person-engineer"}, companyOfFour(), nil)
	if len(layers) != 0 {
		t.Fatalf("expected no layer while somebody outside leadership is in the room, got %+v", layers)
	}
}

func TestARoomOfEveryoneKeepsWhatItHearsForTheCompany(t *testing.T) {
	layers := memory.LayersReadByExactly([]string{"person-chief", "person-director", "person-engineer", "person-designer"}, companyOfFour(), nil)
	if !slices.Equal(layers, []memory.Scope{memory.WorkspaceScope()}) {
		t.Fatalf("expected the company's layer alone, got %+v", layers)
	}
}

func TestAConversationWithOnePersonKeepsWhatItHearsForThem(t *testing.T) {
	layers := memory.LayersReadByExactly([]string{"person-engineer"}, companyOfFour(), nil)
	if !slices.Equal(layers, []memory.Scope{memory.PersonScope("person-engineer")}) {
		t.Fatalf("expected the person's own layer, got %+v", layers)
	}
}

func TestTheAdminCircleIsNeverALayer(t *testing.T) {
	layers := memory.LayersReadByExactly([]string{"person-chief"}, companyOfFour(), nil)
	if slices.Contains(layers, memory.CircleScope("admin")) {
		t.Fatalf("expected no admin layer, got %+v", layers)
	}
}

func TestSomebodyTheDirectoryDoesNotHoldClosesEveryLayer(t *testing.T) {
	layers := memory.LayersReadByExactly([]string{"person-chief", "person-director", "person-stranger"}, companyOfFour(), nil)
	if len(layers) != 0 {
		t.Fatalf("expected no layer while the room holds somebody unknown, got %+v", layers)
	}
}

func TestACircleReadFromTheCircleHoldingItNeedsThoseReadersInTheRoom(t *testing.T) {
	containedCircles := map[string][]string{"leadership": {"platform"}}
	layers := memory.LayersReadByExactly([]string{"person-engineer", "person-designer"}, companyOfFour(), containedCircles)
	if len(layers) != 0 {
		t.Fatalf("expected no platform layer while leadership, which reads it, is not in the room, got %+v", layers)
	}
	layers = memory.LayersReadByExactly([]string{"person-chief", "person-director", "person-engineer", "person-designer"}, companyOfFour(), containedCircles)
	if !slices.Equal(layers, []memory.Scope{memory.WorkspaceScope()}) {
		t.Fatalf("expected the company's layer alone once everyone is in, got %+v", layers)
	}
}
