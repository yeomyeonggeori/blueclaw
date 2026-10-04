package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

type keptFilesActor struct {
	spillWorkspaceActor
	written map[string][]byte
}

func (actor *keptFilesActor) WriteFile(_ context.Context, filePath string, content []byte) error {
	if actor.writeError != nil {
		return actor.writeError
	}
	actor.written[filePath] = content
	return nil
}

type keptFilesActorFactory struct {
	actor            *keptFilesActor
	requestedPersons []string
}

func (factory *keptFilesActorFactory) Requester(_ context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	factory.requestedPersons = append(factory.requestedPersons, request.PersonAccess.PersonID)
	return factory.actor, nil
}

func (factory *keptFilesActorFactory) CanListDirectory(context.Context) bool { return true }

const companyProfileAnswer = `{"tool":"company_info_get","result":{"name":"주식회사 예시","sealImage":"seal.png"}}`

func aCompanyProfileAnswer() mcp.ToolResult {
	return mcp.ToolResult{
		Content: []json.RawMessage{
			json.RawMessage(`{"type":"text","text":` + quoted(companyProfileAnswer) + `}`),
			json.RawMessage(`{"type":"resource","resource":{"uri":"internkim://files/company-profile.json","mimeType":"application/json","text":"{\"name\":\"주식회사 예시\",\"sealImage\":\"seal.png\"}"}}`),
			json.RawMessage(`{"type":"resource","resource":{"uri":"internkim://files/seal.png","mimeType":"image/png","blob":"YSBzZWFs"}}`),
		},
		StructuredContent: json.RawMessage(companyProfileAnswer),
	}
}

func quoted(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded)
}

func callingTheCompanyProfile(t *testing.T, answered mcp.ToolResult, actor *keptFilesActor, taskRunID string) (toolcontract.ToolResult, *keptFilesActorFactory) {
	t.Helper()
	descriptor := aDescriptor("company_info_get", capability.AnsweredByRecord)
	descriptor.SideEffectClass = toolcontract.ToolSideEffectRead
	descriptor.ResultContract = &capability.ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"sealImage":{"type":"string"}},"required":["name"],"additionalProperties":false}`),
	}
	standIn := &recordCatalogStandIn{discovered: []capability.ToolDescriptor{descriptor}, answered: answered}
	toolCatalogBuilder, _ := aBuilderWith(descriptor)
	factory := &keptFilesActorFactory{actor: actor}
	toolCatalogBuilder.UseWorkspaceActorFactory(factory)
	request := aRequestFrom(standIn, "sample@example.test")
	request.PersonAccess = policy.PersonAccess{PersonID: "person-7"}
	toolSet := toolCatalogBuilder.BuildToolSet(request)

	result, errorValue := toolSet.InvokeInternal(toolcontract.WithTaskRunID(context.Background(), taskRunID), toolcontract.ToolInvocation{
		ToolName: "company_info_get",
		Input:    json.RawMessage(`{"title":"ko"}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return result, factory
}

func aKeptFilesActor() *keptFilesActor {
	return &keptFilesActor{written: map[string][]byte{}}
}

func TestAFileTheRecordAnswersIsKeptInTheTaskDirectoryAsTheRequester(t *testing.T) {
	actor := aKeptFilesActor()

	result, factory := callingTheCompanyProfile(t, aCompanyProfileAnswer(), actor, "task-3")

	if len(factory.requestedPersons) == 0 || slices.ContainsFunc(factory.requestedPersons, func(person string) bool { return person != "person-7" }) {
		t.Fatalf("the files were written as %v", factory.requestedPersons)
	}
	directory := "/workspace/private/people/person-7/tmp/tasks/task-3/answered/company_info_get-"
	seal, profile := "", ""
	for filePath, content := range actor.written {
		if strings.HasPrefix(filePath, directory) && strings.HasSuffix(filePath, "/seal.png") {
			seal = string(content)
		}
		if strings.HasPrefix(filePath, directory) && strings.HasSuffix(filePath, "/company-profile.json") {
			profile = string(content)
		}
	}
	if seal != "a seal" || !strings.Contains(profile, `"sealImage":"seal.png"`) {
		t.Fatalf("the files kept were %v", keysOf(actor.written))
	}
	if result.Failed() {
		t.Fatalf("the call failed: %+v", result.Failure)
	}
	if string(result.Output.Data) != `{"name":"주식회사 예시","sealImage":"seal.png"}` {
		t.Fatalf("the result the contract sees was %s", result.Output.Data)
	}
}

func TestTheModelSeesWhereAnAnsweredFileIsAndNeverItsBytes(t *testing.T) {
	result, _ := callingTheCompanyProfile(t, aCompanyProfileAnswer(), aKeptFilesActor(), "task-3")

	seen := result.Output.Content
	if strings.Contains(seen, "YSBzZWFs") {
		t.Fatalf("the seal's bytes reached the model: %s", seen)
	}
	if !strings.Contains(seen, `"type":"resource_link"`) || !strings.Contains(seen, `file:///workspace/private/people/person-7/tmp/tasks/task-3/answered/company_info_get-`) {
		t.Fatalf("the model was not told where the files are: %s", seen)
	}
	if !strings.Contains(seen, `"name":"seal.png"`) || !strings.Contains(seen, `"name":"company-profile.json"`) {
		t.Fatalf("the model was not told which files were kept: %s", seen)
	}
}

func TestAFileThatCannotBeKeptIsNamedAndItsBytesStillStayOut(t *testing.T) {
	actor := aKeptFilesActor()
	actor.writeError = errors.New("permission denied")

	result, _ := callingTheCompanyProfile(t, aCompanyProfileAnswer(), actor, "task-3")

	seen := result.Output.Content
	if strings.Contains(seen, "YSBzZWFs") {
		t.Fatalf("the seal's bytes reached the model: %s", seen)
	}
	if !strings.Contains(seen, "seal.png as a file, and it could not be kept: permission denied") {
		t.Fatalf("the model was not told the seal was lost: %s", seen)
	}
}

func TestAFileAnsweredOutsideATaskIsNamedAsNotKept(t *testing.T) {
	actor := aKeptFilesActor()

	result, _ := callingTheCompanyProfile(t, aCompanyProfileAnswer(), actor, "")

	if len(actor.written) != 0 {
		t.Fatalf("files were written with no task to keep them in: %v", keysOf(actor.written))
	}
	if !strings.Contains(result.Output.Content, "company-profile.json as a file, and it could not be kept") {
		t.Fatalf("the model was not told: %s", result.Output.Content)
	}
}

func TestAnAnsweredFileNameNeverLeavesItsDirectory(t *testing.T) {
	cases := map[string]string{
		"internkim://files/..%2F..%2Fnotes.txt": "notes.txt",
		"internkim://files/../seal.png":         "seal.png",
		"internkim://files/":                    "",
		"internkim://files/..":                  "",
	}
	for uri, expected := range cases {
		if name := answeredFileName(uri); name != expected {
			t.Errorf("%s was named %q, expected %q", uri, name, expected)
		}
	}
}

func TestAnAnswerWithNoFileIsLeftAsItCame(t *testing.T) {
	actor := aKeptFilesActor()
	answered := mcp.ToolResult{
		Content:           []json.RawMessage{json.RawMessage(`{"type":"text","text":"{}"}`)},
		StructuredContent: json.RawMessage(`{"tool":"company_info_get","result":{"name":"주식회사 예시"}}`),
	}

	result, factory := callingTheCompanyProfile(t, answered, actor, "task-3")

	if len(factory.requestedPersons) != 0 || len(actor.written) != 0 {
		t.Fatalf("an answer with no file wrote %v", keysOf(actor.written))
	}
	if !strings.Contains(result.Output.Content, `{"type":"text","text":"{}"}`) {
		t.Fatalf("the answer changed: %s", result.Output.Content)
	}
}

func keysOf(written map[string][]byte) []string {
	keys := make([]string, 0, len(written))
	for key := range written {
		keys = append(keys, key)
	}
	return keys
}
