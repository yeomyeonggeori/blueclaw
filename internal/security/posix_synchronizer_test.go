package security

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func TestPOSIXSynchronizerPassesComputedStateDocumentToHelper(t *testing.T) {
	rootPath := t.TempDir()
	observedStatePath := filepath.Join(rootPath, "observed-state.json")
	helperPath := filepath.Join(rootPath, "helper")
	helperDocument := "#!/bin/sh\nset -eu\nstate_path=\"\"\nwhile [ \"$#\" -gt 0 ]; do\ncase \"$1\" in\n--state) state_path=\"$2\"; shift 2 ;;\n--workspace) shift 2 ;;\n*) shift ;;\nesac\ndone\nif [ -z \"$state_path\" ]; then exit 7; fi\ncat \"$state_path\" > \"" + observedStatePath + "\"\n"
	if errorValue := os.WriteFile(helperPath, []byte(helperDocument), 0700); errorValue != nil {
		t.Fatal(errorValue)
	}

	policyPath := filepath.Join(rootPath, "policy.json")
	policyDocument := policy.PolicyDocument{
		People: []policy.PersonPolicy{{
			PersonID: "id_22120f1e_432e6dde",
			Circles:  []string{policy.MemberCircleID},
		}},
	}
	document, errorValue := json.Marshal(policyDocument)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(policyPath, document, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	synchronizer := NewPOSIXSynchronizer(config.TerminalConfiguration{
		POSIXHelperPath:   helperPath,
		WorkspaceRootPath: "/workspace",
	}, policyPath)
	if errorValue := synchronizer.Synchronize(context.Background()); errorValue != nil {
		t.Fatal(errorValue)
	}

	observedDocument, errorValue := os.ReadFile(observedStatePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var state POSIXState
	if errorValue := json.Unmarshal(observedDocument, &state); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !containsPOSIXDirectory(state, "/workspace/circles/member/sites", "blueclaw", "bc_circle_member", "2770") {
		t.Fatalf("expected member sites directory in state, got %+v", state.Directories)
	}
}

func TestPOSIXRequesterWorkspaceProvisionerProjectsRequesterAccessOutsidePolicy(t *testing.T) {
	rootPath := t.TempDir()
	observedStatePath := filepath.Join(rootPath, "observed-state.json")
	helperPath := filepath.Join(rootPath, "helper")
	helperDocument := fmt.Sprintf(`#!/bin/sh
set -eu
command_name="$1"
shift
case "$command_name" in
sync)
  state_path=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
    --state) state_path="$2"; shift 2 ;;
    --workspace) shift 2 ;;
    *) shift ;;
    esac
  done
  if [ -z "$state_path" ]; then exit 7; fi
  cat "$state_path" > %q
  ;;
reconcile-home)
  ;;
*)
  exit 9
  ;;
esac
`, observedStatePath)
	if errorValue := os.WriteFile(helperPath, []byte(helperDocument), 0700); errorValue != nil {
		t.Fatal(errorValue)
	}

	policyPath := filepath.Join(rootPath, "policy.json")
	document, errorValue := json.Marshal(policy.PolicyDocument{})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(policyPath, document, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	synchronizer := NewPOSIXSynchronizer(config.TerminalConfiguration{
		POSIXHelperPath:   helperPath,
		WorkspaceRootPath: "/workspace",
	}, policyPath)
	provisioner := NewPOSIXRequesterWorkspaceProvisioner(synchronizer)
	errorValue = provisioner.ProvisionRequesterWorkspace(context.Background(), policy.PersonAccess{
		PersonID: "person-1",
		Circles:  []string{"finance", "admin"},
	}, "/workspace")
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	observedDocument, errorValue := os.ReadFile(observedStatePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var state POSIXState
	if errorValue := json.Unmarshal(observedDocument, &state); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !hasPOSIXUserGroup(state, "bc_person_person-1", "bc_circle_member") {
		t.Fatalf("expected requester to inherit member group, got %+v", state.Users)
	}
	if !hasPOSIXUserGroup(state, "bc_person_person-1", "bc_circle_finance") {
		t.Fatalf("expected requester finance circle group, got %+v", state.Users)
	}
	if hasPOSIXUserGroup(state, "bc_person_person-1", "bc_circle_admin") {
		t.Fatalf("expected requester POSIX identity to omit admin group, got %+v", state.Users)
	}
	if !containsPOSIXDirectory(state, "/workspace/circles/member/sites", "blueclaw", "bc_circle_member", "2770") {
		t.Fatalf("expected member sites directory, got %+v", state.Directories)
	}
	if !containsPOSIXDirectory(state, "/workspace/circles/finance", "blueclaw", "bc_circle_finance", "2770") {
		t.Fatalf("expected requester circle directory, got %+v", state.Directories)
	}
}

func containsPOSIXDirectory(state POSIXState, path string, owner string, group string, modeText string) bool {
	for _, directory := range state.Directories {
		if directory.Path == path && directory.Owner == owner && directory.Group == group && directory.ModeText == modeText {
			return true
		}
	}
	return false
}
