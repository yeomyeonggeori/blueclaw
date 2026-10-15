package memory

import (
	"cmp"
	"maps"
	"slices"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

// LayersReadByExactly names the layers a conversation may keep what it heard
// in: those whose readers are exactly the people in it. A layer somebody
// outside the room reads would tell them what they never heard, and a layer
// read by fewer people than the room holds would take a note from somebody who
// can neither read it nor correct it.
//
// The company's layer, when it qualifies, is the only one named: a circle that
// everyone reads says nothing the company's layer does not.
func LayersReadByExactly(roomPersonIDs []string, everyone []policy.PersonAccess, containedCircles map[string][]string) []Scope {
	room := map[string]bool{}
	for _, personID := range roomPersonIDs {
		room[personID] = true
	}
	if len(room) == 0 {
		return nil
	}
	layers := []Scope{}
	for scope, readers := range readersByScope(everyone, containedCircles) {
		if maps.Equal(readers, room) {
			layers = append(layers, scope)
		}
	}
	if slices.Contains(layers, WorkspaceScope()) {
		return []Scope{WorkspaceScope()}
	}
	slices.SortFunc(layers, func(left Scope, right Scope) int {
		return cmp.Or(cmp.Compare(breadth(right), breadth(left)), cmp.Compare(left.ID, right.ID))
	})
	return layers
}

func readersByScope(everyone []policy.PersonAccess, containedCircles map[string][]string) map[Scope]map[string]bool {
	readers := map[Scope]map[string]bool{}
	for _, personAccess := range everyone {
		for _, scope := range ScopesToSearch(personAccess, containedCircles) {
			if readers[scope] == nil {
				readers[scope] = map[string]bool{}
			}
			readers[scope][personAccess.PersonID] = true
		}
	}
	return readers
}
