//go:build !nobundledharness

package agentruntime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
)

func TestALaunchTellsTheAgentTheRequestersLanguage(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		messageLanguage  string
		personaDocument  string
		companyLocale    string
		expectedLanguage string
	}{
		{name: "the company's locale when the requester chose nothing", companyLocale: "en", expectedLanguage: "en"},
		{name: "the requester's own choice over the company's", personaDocument: `{"schemaVersion": 1, "language": {"default": "ja"}}`, companyLocale: "en", expectedLanguage: "ja"},
		{name: "a language the message carries over both", messageLanguage: "ko", personaDocument: `{"schemaVersion": 1, "language": {"default": "ja"}}`, companyLocale: "en", expectedLanguage: "ko"},
		{name: "nothing when nobody has said", expectedLanguage: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := task.NewTaskRunService(task.NewTaskEventService())
			harness := harnesstest.New(taskRunService)
			toolCatalogBuilder := NewToolCatalogBuilder()
			workspacePath := t.TempDir()
			toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
			documents := map[string][]byte{}
			if testCase.personaDocument != "" {
				documents[filepath.Join(security.PersonHomeDirectoryPath(workspacePath, "person-1"), ".internkim", "user.json")] = []byte(testCase.personaDocument)
			}
			toolCatalogBuilder.UseWorkspaceActorFactory(&personaActorFactory{documents: documents})
			taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
			taskLauncher.UseCompanyLocaleProvider(func() string { return testCase.companyLocale })

			_, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
				Source:                    TaskLaunchSourceConnector,
				RequesterPersonID:         "person-1",
				ConversationID:            "channel-1",
				Prompt:                    "Send Sam Sample a data room link.",
				ResponseLanguage:          testCase.messageLanguage,
				PersonAccess:              policy.PersonAccess{PersonID: "person-1"},
				AccessibleConversationIDs: []string{"channel-1"},
			})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if language := harness.LastTurnRequest().ResponseLanguage; language != testCase.expectedLanguage {
				t.Fatalf("expected the turn to be written in %q, got %q", testCase.expectedLanguage, language)
			}
		})
	}
}
