package agentruntime

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type officeContextFixture struct {
	workspacePath string
	builder       *ToolCatalogBuilder
	request       ToolCatalogRequest
	taskRun       agentcontract.TaskRun
	standIn       *recordCatalogStandIn
}

func newOfficeContextFixture(t *testing.T, descriptors ...capability.ToolDescriptor) officeContextFixture {
	t.Helper()
	workspacePath := t.TempDir()
	terminalService := security.NewShellService(config.TerminalConfiguration{
		WorkspaceRootPath:     workspacePath,
		Mode:                  "native",
		TimeoutSecond:         5,
		OutputMaxBytes:        65536,
		SessionMaxCount:       2,
		AllowInteractiveShell: true,
	})
	builder, _ := aBuilderWith(descriptors...)
	builder.UseWorkspaceRootPath(workspacePath)
	builder.UseTerminalService(terminalService)
	builder.UseWorkspaceActorFactory(security.NewDirectWorkspaceActorFactory(terminalService))
	allowedToolNames := internalTestToolNames()
	for _, descriptor := range descriptors {
		allowedToolNames = append(allowedToolNames, descriptor.Name)
	}
	builder.UseAllowedToolNamesByProfile(nil, allowedToolNames)
	builder.UseCompanyProvider(func() agentcontract.CompanyContext { return agentcontract.CompanyContext{TimeZone: "Asia/Seoul"} })
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	builder.UseTaskRunService(taskRunService)
	standIn := &recordCatalogStandIn{discovered: descriptors}
	request := aRequestFrom(standIn, "sample@example.com")
	request.ProfileName = "default"
	request.RequesterPersonID = "person-1"
	request.RequesterName = "이샘플"
	request.ConversationID = "dm:channel-1"
	request.PersonAccess = policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}}
	return officeContextFixture{
		workspacePath: workspacePath,
		builder:       builder,
		request:       request,
		taskRun:       taskRunService.CreateTaskRun("person-1", "dm:channel-1", "견적서 만들어 줘"),
		standIn:       standIn,
	}
}

func (fixture officeContextFixture) homePath() string {
	return filepath.Join(fixture.workspacePath, "private", "people", "person-1")
}

func (fixture officeContextFixture) taskContext() context.Context {
	return toolcontract.WithTaskRunID(context.Background(), fixture.taskRun.TaskRunID)
}

func (fixture officeContextFixture) invoke(t *testing.T, toolName string, input any) toolcontract.ToolResult {
	t.Helper()
	toolSet := fixture.builder.BuildToolSet(fixture.request)
	result, errorValue := toolSet.InvokeInternal(fixture.taskContext(), toolcontract.ToolInvocation{ToolName: toolName, Input: toolcontract.MarshalToolInput(input)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return result
}

func (fixture officeContextFixture) contextTheShellReads(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	result := fixture.invoke(t, "bash", map[string]any{"command": `cat "$OFFICE_RUNTIME_CONTEXT"`})
	if result.Failed() {
		t.Fatalf("the shell could not read the office runtime context: %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	document := map[string]json.RawMessage{}
	if errorValue := json.Unmarshal([]byte(commandResult.Stdout), &document); errorValue != nil {
		t.Fatalf("the office runtime context is not JSON: %q", commandResult.Stdout)
	}
	return document
}

func TestTheShellIsToldWhereTheOfficeRuntimeContextIsAndFindsTheTaskFactsThere(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	attachmentPath := filepath.Join(fixture.homePath(), "inbox", "budget.csv")
	fixture.request.VisibleContext = agentcontract.VisibleContext{CurrentMaterials: []agentcontract.VisibleContextMaterial{{MaterialID: "m-1", Filename: "budget.csv", Path: attachmentPath, IsAvailable: true}}}

	document := fixture.contextTheShellReads(t)

	seoul, _ := time.LoadLocation("Asia/Seoul")
	expected := map[string]string{
		"requester":           `{"name":"이샘플","email":"sample@example.com"}`,
		"today":               `"` + fixture.taskRun.CreatedAt.In(seoul).Format(time.DateOnly) + `"`,
		"company":             `{}`,
		"registeredDocuments": `[]`,
		"attachments":         `[{"name":"budget.csv","path":` + quoted(attachmentPath) + `}]`,
	}
	for name, value := range expected {
		if string(document[name]) != value {
			t.Fatalf("%s was %s, expected %s", name, document[name], value)
		}
	}
}

func TestTheRuntimeContextSaysTheHostReviewsDeckRendersOnlyWhileBothReviewModelsAreSet(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	if reviews := string(fixture.contextTheShellReads(t)["reviewsDeckRenders"]); reviews != "false" {
		t.Fatalf("a host with no review models told the office it reviews deck renders: %s", reviews)
	}
	models := &deckModels{}
	fixture.builder.UseVisualReviewModels(models, nil)
	if reviews := string(fixture.contextTheShellReads(t)["reviewsDeckRenders"]); reviews != "false" {
		t.Fatalf("a host with no fixer told the office it reviews deck renders: %s", reviews)
	}
	fixture.builder.UseVisualReviewModels(models, models)
	if reviews := string(fixture.contextTheShellReads(t)["reviewsDeckRenders"]); reviews != "true" {
		t.Fatalf("a host with both review models did not tell the office it reviews deck renders: %s", reviews)
	}
}

func TestTheModelCannotPointTheOfficeAtAnotherRuntimeContext(t *testing.T) {
	if !security.IsWorkspaceManagedEnvironmentName(officeContract.RuntimeContextVariable) {
		t.Fatalf("%s is not managed by the workspace, so a command's own environment could replace it", officeContract.RuntimeContextVariable)
	}
}

func companyInfoGetDescriptor() capability.ToolDescriptor {
	descriptor := aDescriptor(companyInfoGetToolName, capability.AnsweredByRecord)
	descriptor.SideEffectClass = toolcontract.ToolSideEffectRead
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"language":{"type":"string"}},"additionalProperties":false}`)
	descriptor.InputIntentSchema = descriptor.InputSchema
	descriptor.ResultContract = &capability.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"sealImage":{"type":"string"}},"additionalProperties":false}`)}
	return descriptor
}

func companyDocumentRegisterDescriptor() capability.ToolDescriptor {
	descriptor := aDescriptor(companyDocumentRegisterToolName, capability.AnsweredByRecord)
	descriptor.SideEffectClass = toolcontract.ToolSideEffectStateChange
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"documentType":{"type":"string"}},"additionalProperties":false}`)
	descriptor.InputIntentSchema = descriptor.InputSchema
	descriptor.ResultContract = &capability.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"documentID":{"type":"string"},"documentNumber":{"type":["string","null"]}},"additionalProperties":false}`)}
	return descriptor
}

func TestTheCompanyProfileARecordAnswerKeptIsBoundForItsLanguage(t *testing.T) {
	fixture := newOfficeContextFixture(t, companyInfoGetDescriptor())
	fixture.standIn.answered = aCompanyProfileAnswer()

	if result := fixture.invoke(t, companyInfoGetToolName, map[string]string{"language": "en"}); result.Failed() {
		t.Fatalf("company_info_get failed: %+v", result.Failure)
	}

	var company map[string]string
	_ = json.Unmarshal(fixture.contextTheShellReads(t)["company"], &company)
	profilePath := company["en"]
	if !strings.HasPrefix(profilePath, filepath.Join(fixture.homePath(), "tmp", "tasks", fixture.taskRun.TaskRunID, "answered")) || filepath.Base(profilePath) != companyProfileFileName {
		t.Fatalf("the company binding was %v", company)
	}
	if _, errorValue := os.Stat(filepath.Join(filepath.Dir(profilePath), "seal.png")); errorValue != nil {
		t.Fatalf("the seal is not beside the bound profile: %v", errorValue)
	}
}

func TestEachDocumentNumberTheRecordRegistersIsAppendedInOrder(t *testing.T) {
	fixture := newOfficeContextFixture(t, companyDocumentRegisterDescriptor())
	for _, documentNumber := range []string{"QT-2026-0001", "QT-2026-0002"} {
		fixture.standIn.answered = mcp.ToolResult{StructuredContent: json.RawMessage(`{"tool":"company_document_register","result":{"documentID":"d","documentNumber":"` + documentNumber + `"}}`)}
		if result := fixture.invoke(t, companyDocumentRegisterToolName, map[string]string{"documentType": "quote"}); result.Failed() {
			t.Fatalf("company_document_register failed: %+v", result.Failure)
		}
	}
	fixture.standIn.answered = mcp.ToolResult{StructuredContent: json.RawMessage(`{"tool":"company_document_register","result":{"documentID":"e","documentNumber":null}}`)}
	fixture.invoke(t, companyDocumentRegisterToolName, map[string]string{"documentType": "received"})

	document := fixture.contextTheShellReads(t)
	if string(document["registeredDocuments"]) != `[{"documentNumber":"QT-2026-0001"},{"documentNumber":"QT-2026-0002"}]` {
		t.Fatalf("the registered documents were %s", document["registeredDocuments"])
	}
}

func TestTheRuntimeContextHoldsExactlyTheFieldsTheOfficeContractNames(t *testing.T) {
	var contract struct {
		RuntimeContext contractSchema `json:"runtimeContext"`
	}
	if errorValue := json.Unmarshal(officeHostContractDocument, &contract); errorValue != nil {
		t.Fatal(errorValue)
	}
	written := officeRuntimeContext{
		Company:             map[string]string{"ko": "/p/company-profile.json"},
		RegisteredDocuments: []officeRegisteredDocument{{DocumentNumber: "N"}},
		Attachments:         []officeAttachment{{Name: "a.csv", Path: "/p/a.csv"}},
	}
	var document map[string]json.RawMessage
	_ = json.Unmarshal([]byte(MarshalBody(written)), &document)
	requireSameNames(t, "runtime context", namesOf(document), contract.RuntimeContext.propertyNames())
	requireSameNames(t, "required fields", contract.RuntimeContext.Required, contract.RuntimeContext.propertyNames())
	for name, nested := range map[string]json.RawMessage{"requester": document["requester"], "registeredDocuments": document["registeredDocuments"], "attachments": document["attachments"]} {
		requireSameNames(t, name, objectKeys(nested), contract.RuntimeContext.Properties[name].memberNames())
	}
}

type contractSchema struct {
	Properties map[string]contractSchema `json:"properties"`
	Items      *contractSchema           `json:"items"`
	Required   []string                  `json:"required"`
}

func (schema contractSchema) propertyNames() []string {
	return namesOf(schema.Properties)
}

func (schema contractSchema) memberNames() []string {
	if schema.Items != nil {
		return schema.Items.propertyNames()
	}
	return schema.propertyNames()
}

func objectKeys(encoded json.RawMessage) []string {
	var object map[string]json.RawMessage
	if json.Unmarshal(encoded, &object) == nil {
		return namesOf(object)
	}
	var objects []map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &objects)
	if len(objects) == 0 {
		return nil
	}
	return namesOf(objects[0])
}

func requireSameNames(t *testing.T, label string, actual []string, expected []string) {
	t.Helper()
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("%s: Go writes %v and the office contract names %v", label, actual, expected)
	}
}

func namesOf[Value any](object map[string]Value) []string {
	return slices.Collect(maps.Keys(object))
}

type fixedTaskRunRepository struct {
	task.TaskRunRepository
	taskRun agentcontract.TaskRun
}

func (repository fixedTaskRunRepository) FindTaskRun(taskRunID string) (agentcontract.TaskRun, bool, error) {
	return repository.taskRun, taskRunID == repository.taskRun.TaskRunID, nil
}

func TestTodayIsTheDateInTheCompanysTimeZoneWhenTheTaskStarted(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	startedAt := fixture.taskRun
	startedAt.CreatedAt = time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC)
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.UseRepository(fixedTaskRunRepository{taskRun: startedAt})
	fixture.builder.UseTaskRunService(taskRunService)
	fixture.builder.UseCompanyProvider(func() agentcontract.CompanyContext {
		return agentcontract.CompanyContext{TimeZone: "America/Los_Angeles"}
	})

	document := fixture.contextTheShellReads(t)

	if string(document["today"]) != `"2026-10-03"` {
		t.Fatalf("a task started at 02:00 UTC on 4 October is dated %s for a company in Los Angeles", document["today"])
	}
}
