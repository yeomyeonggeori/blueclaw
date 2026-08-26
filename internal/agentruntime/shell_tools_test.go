package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func TestTerminalRunTranslatesAgentWorkspacePaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command":              "mkdir -p build && printf ok > build/result.txt",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected shell success, got %s", result.ContentText())
	}
	var resultDocument terminalCommandResultDocument
	if errorValue := json.Unmarshal(result.Output.Data, &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultDocument.Mode != terminalRunModeCommand || !resultDocument.Completed || resultDocument.ExitCode != 0 || resultDocument.TimedOut {
		t.Fatalf("expected canonical completed command result, got %+v", resultDocument)
	}
	if len(result.Effects) != 0 {
		t.Fatalf("expected shell to avoid inferred resource effects, got %+v", result.Effects)
	}
	content, errorValue := os.ReadFile(filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck", "build", "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(content) != "ok" {
		t.Fatalf("expected translated workspace command to write file, got %q", string(content))
	}
}

func TestTerminalRunCommandRequestUsesCanonicalFields(t *testing.T) {
	input := terminalRunToolInput{
		Command:              "printf ok",
		WorkingDirectoryPath: "/workspace",
		TimeoutSecond:        30,
	}

	commandRequest := input.commandRequest()
	if commandRequest.Command != input.Command ||
		commandRequest.WorkingDirectoryPath != input.WorkingDirectoryPath ||
		commandRequest.TimeoutSecond != input.TimeoutSecond {
		t.Fatalf("expected canonical command request, got %+v", commandRequest)
	}
}

func TestTerminalRunRejectsInvalidInputShapes(t *testing.T) {
	toolRegistry := newTerminalToolTestCatalogBuilder(t.TempDir()).BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})
	testCases := []struct {
		name  string
		input json.RawMessage
	}{
		{name: "unknown field", input: json.RawMessage(`{"command":"true","unknown":true}`)},
		{name: "fractional timeout", input: json.RawMessage(`{"command":"true","timeoutSecond":1.5}`)},
		{name: "zero timeout", input: json.RawMessage(`{"command":"true","timeoutSecond":0}`)},
		{name: "negative timeout", input: json.RawMessage(`{"command":"true","timeoutSecond":-1}`)},
		{name: "missing command", input: json.RawMessage(`{}`)},
		{name: "legacy mode", input: json.RawMessage(`{"mode":"command","command":"true"}`)},
		{name: "legacy executable", input: json.RawMessage(`{"executableName":"true"}`)},
		{name: "legacy arguments", input: json.RawMessage(`{"command":"true","arguments":["one"]}`)},
		{name: "legacy stdin", input: json.RawMessage(`{"command":"true","stdin":"text"}`)},
		{name: "legacy environment", input: json.RawMessage(`{"command":"true","environmentVariables":{"PATH":"/tmp"}}`)},
		{name: "legacy session", input: json.RawMessage(`{"mode":"session_status","sessionID":"session-1"}`)},
		{name: "approval without reason", input: json.RawMessage(`{"command":"true","approvalRequired":true}`)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
				ToolName: toolcontract.ShellToolName,
				Input:    testCase.input,
			})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.InvalidInput.String() {
				t.Fatalf("expected invalid input failure, got %+v", result)
			}
		})
	}
}

func TestTerminalRunAcceptsUnusedApprovalReason(t *testing.T) {
	toolRegistry := newTerminalToolTestCatalogBuilder(t.TempDir()).BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result := invokeTerminalRunTestTool(t, toolRegistry, json.RawMessage(`{"command":"true","approvalRequired":false,"approvalReason":"No approval needed."}`))
	var resultDocument terminalCommandResultDocument
	decodeTerminalRunTestData(t, result, &resultDocument)
	if !resultDocument.Completed {
		t.Fatalf("expected harmless unused approval reason to remain executable, got %+v", resultDocument)
	}
}

func TestTerminalRunAcceptsApprovalFieldsAfterTheApprovalGate(t *testing.T) {
	toolRegistry := newTerminalToolTestCatalogBuilder(t.TempDir()).BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result := invokeTerminalRunTestTool(t, toolRegistry, json.RawMessage(`{"command":"true","approvalRequired":true,"approvalReason":"Publish the release."}`))
	var resultDocument terminalCommandResultDocument
	decodeTerminalRunTestData(t, result, &resultDocument)
	if !resultDocument.Completed {
		t.Fatalf("expected approved command shape to remain executable after the approval gate, got %+v", resultDocument)
	}
}

func TestTerminalRunFailureHasCanonicalData(t *testing.T) {
	toolRegistry := newTerminalToolTestCatalogBuilder(t.TempDir()).BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.ShellToolName,
		Input:    json.RawMessage(`{"command":"exit 7"}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected command failure, got %+v", result)
	}
	var resultDocument terminalCommandResultDocument
	if errorValue := json.Unmarshal(result.Output.Data, &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultDocument.Mode != terminalRunModeCommand || resultDocument.Completed || resultDocument.ExitCode != 7 {
		t.Fatalf("expected canonical failed command result, got %+v", resultDocument)
	}
}

func invokeTerminalRunTestTool(t *testing.T, toolRegistry *toolcontract.ToolSet, input json.RawMessage) toolcontract.ToolResult {
	t.Helper()
	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: toolcontract.ShellToolName, Input: input})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected shell success, got %+v", result)
	}
	return result
}

func decodeTerminalRunTestData(t *testing.T, result toolcontract.ToolResult, target any) {
	t.Helper()
	if errorValue := json.Unmarshal(result.Output.Data, target); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestTerminalRunAllowsStderrRedirection(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command":              "printf ok 2>&1",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected shell stderr redirection success, got %s", result.ContentText())
	}
}

func TestTerminalRunAllowsSourceFileWrite(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command":              "printf 'export default function App(){}' > App.tsx",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("shell must allow writing a source file directly, got %s", result.ContentText())
	}
}

func TestTerminalRunAllowsServiceOwnedPathText(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command": "printf '%s' /workspace/.blueclaw/tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected service-owned path text not to be policy-blocked, got %+v", result)
	}
}

func TestTerminalRunDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command": "pwd",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected shell success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedSuffix := filepath.Join("private", "people", "person-1")
	if !strings.HasSuffix(strings.TrimSpace(commandResult.Stdout), expectedSuffix) {
		t.Fatalf("expected terminal cwd at the requester home %s, got %q", expectedSuffix, commandResult.Stdout)
	}
}

func TestTerminalRunMaterializesRequesterRuntimeEnvironment(t *testing.T) {
	workspacePath := t.TempDir()
	builtinSkillsPythonPath := filepath.Join(t.TempDir(), "bin", "python")
	t.Setenv("BLUECLAW_BUILTIN_SKILLS_PYTHON", builtinSkillsPythonPath)
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command": `test -d "$TMPDIR" && test -d "$BUN_TMPDIR" && test -d "$BUN_INSTALL" && printf '%s\n%s\n%s\n%s\n%s\n%s' "$HOME" "$PATH" "$TMPDIR" "$BUN_TMPDIR" "$BUN_INSTALL" "$BLUECLAW_BUILTIN_SKILLS_PYTHON"`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected shell success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	requesterRootPath := filepath.Join(workspacePath, "private", "people", "person-1")
	expectedValues := []string{
		requesterRootPath,
		security.CanonicalRuntimePATH,
		filepath.Join(requesterRootPath, "tmp", ".runtime", "tmp"),
		filepath.Join(requesterRootPath, "tmp", ".runtime", "bun", "tmp"),
		filepath.Join(requesterRootPath, "tmp", ".runtime", "bun", "install"),
		builtinSkillsPythonPath,
	}
	actualValues := strings.Split(strings.TrimSpace(commandResult.Stdout), "\n")
	if !slices.Equal(actualValues, expectedValues) {
		t.Fatalf("expected exact runtime environment paths %v, got %v", expectedValues, actualValues)
	}
	for _, expectedText := range expectedValues {
		if expectedText == requesterRootPath ||
			expectedText == security.CanonicalRuntimePATH ||
			expectedText == builtinSkillsPythonPath {
			continue
		}
		if _, errorValue := os.Stat(expectedText); errorValue != nil {
			t.Fatalf("expected runtime environment directory %s: %v", expectedText, errorValue)
		}
	}
}

func TestTerminalRunScopesTaskTemporaryDirectoryToTheTaskRun(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	toolContext := toolcontract.WithTaskRunID(context.Background(), "task-run-1")
	result, errorValue := toolRegistry.Invoke(toolContext, toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command": `printf '%s\n%s\n%s' "$BLUECLAW_REQUESTER_TMP" "$BLUECLAW_TASK_TMP" "$TMPDIR"`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected shell success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	requesterHomePath := filepath.Join(workspacePath, "private", "people", "person-1")
	expectedValues := []string{
		filepath.Join(requesterHomePath, "tmp"),
		filepath.Join(requesterHomePath, "tmp", "tasks", "task-run-1"),
		filepath.Join(requesterHomePath, "tmp", "tasks", "task-run-1", "tmp"),
	}
	actualValues := strings.Split(strings.TrimSpace(commandResult.Stdout), "\n")
	if !slices.Equal(actualValues, expectedValues) {
		t.Fatalf("expected task scoped temporary directories %v, got %v", expectedValues, actualValues)
	}
	if actualValues[0] == actualValues[1] {
		t.Fatalf("expected requester tmp and task tmp to differ, got %v", actualValues)
	}
	if _, errorValue := os.Stat(expectedValues[1]); errorValue != nil {
		t.Fatalf("expected task temporary directory to exist, got %v", errorValue)
	}
}

func TestTerminalRunRelativeWorkingDirectoryUsesConversationDefault(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_write",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "ok",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file_write success, got %s", writeResult.ContentText())
	}

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command":              "pwd && cat input.txt && printf built > result.txt",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected shell success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	if !strings.Contains(commandResult.Stdout, expectedDirectoryPath) || !strings.Contains(commandResult.Stdout, "ok") {
		t.Fatalf("expected terminal cwd and file content under private tmp, got %q", commandResult.Stdout)
	}
	resultDocument, errorValue := os.ReadFile(filepath.Join(expectedDirectoryPath, "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(resultDocument) != "built" {
		t.Fatalf("expected terminal output under private tmp, got %q", string(resultDocument))
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "tmp", "deck")); !os.IsNotExist(errorValue) {
		t.Fatalf("shell must not create workspace-root tmp for relative workingDirectoryPath")
	}
}

func TestTerminalRunFailsWhenPOSIXDeniesCircleWorkingDirectory(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	withoutDirectoryAccess(t, financeDirectoryPath)
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "shell",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"command":              "printf no",
			"workingDirectoryPath": "/workspace/circles/finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(strings.ToLower(result.ContentText()), "permission denied") {
		t.Fatalf("expected the OS denial on the circle working directory to fail shell, got %+v", result)
	}
}
