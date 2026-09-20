package skill

import (
	"reflect"
	"testing"
)

func TestASkillNamesTheToolsItWasNotOffered(t *testing.T) {
	skillBundle := SkillBundle{ToolReferences: []ToolReference{"task_add", "bash", "event_add"}}

	missingToolNames := skillBundle.MissingToolNames([]string{"bash", "task_add"})

	if !reflect.DeepEqual(missingToolNames, []string{"event_add"}) {
		t.Fatalf("missing tools = %v, want [event_add]", missingToolNames)
	}
}

func TestASkillOfferedEveryToolItNeedsNamesNone(t *testing.T) {
	skillBundle := SkillBundle{ToolReferences: []ToolReference{"task_add", " bash "}}

	if missingToolNames := skillBundle.MissingToolNames([]string{"bash", "task_add", "read"}); len(missingToolNames) != 0 {
		t.Fatalf("missing tools = %v, want none", missingToolNames)
	}
}

func TestASkillNeedingNoToolNamesNone(t *testing.T) {
	if missingToolNames := (SkillBundle{}).MissingToolNames(nil); len(missingToolNames) != 0 {
		t.Fatalf("missing tools = %v, want none", missingToolNames)
	}
}
