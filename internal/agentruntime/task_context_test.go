//go:build !nobundledharness

package agentruntime

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func (fixture taskFixture) taskContextTheShellReads(t *testing.T) taskContext {
	t.Helper()
	output := fixture.shellOutput(t, `cat "$SKILL_TASK_CONTEXT"`)
	var document taskContext
	if errorValue := json.Unmarshal([]byte(output), &document); errorValue != nil {
		t.Fatalf("the task context is not JSON: %q", output)
	}
	return document
}

func TestTheShellIsToldWhereTheTaskContextIsAndFindsTheRequestThere(t *testing.T) {
	fixture := newTaskFixture(t)
	attachmentPath := filepath.Join(fixture.homePath(), "inbox", "budget.csv")
	fixture.request.Prompt = "견적서 만들어 줘"
	fixture.request.VisibleContext = agentcontract.VisibleContext{
		Messages:         []agentcontract.VisibleContextMessage{{Text: "지난주 단가표 첨부합니다"}},
		CurrentMaterials: []agentcontract.VisibleContextMaterial{{MaterialID: "m-1", Filename: "budget.csv", Path: attachmentPath, IsAvailable: true, MarkdownPreview: "품목,단가"}},
		Materials:        []agentcontract.VisibleContextMaterial{{MaterialID: "m-0", Filename: "old.pdf"}},
	}

	document := fixture.taskContextTheShellReads(t)

	seoul, _ := time.LoadLocation("Asia/Seoul")
	if document.Requester != (taskRequester{Name: "이샘플", Email: "sample@example.com"}) {
		t.Fatalf("the requester was %+v", document.Requester)
	}
	if document.Today != fixture.taskRun.CreatedAt.In(seoul).Format(time.DateOnly) {
		t.Fatalf("today was %q", document.Today)
	}
	if strings.Join(document.Request, "|") != "지난주 단가표 첨부합니다|견적서 만들어 줘" {
		t.Fatalf("the request was %q", document.Request)
	}
	expected := []taskAttachment{
		{Name: "budget.csv", Path: attachmentPath, Text: "품목,단가", IsCurrent: true},
		{Name: "old.pdf"},
	}
	if len(document.Attachments) != len(expected) || document.Attachments[0] != expected[0] || document.Attachments[1] != expected[1] {
		t.Fatalf("the attachments were %+v", document.Attachments)
	}
}

func TestEveryRecordToolAnswerIsKeptInTheTaskContextWithTheFilesItKept(t *testing.T) {
	fixture := newTaskFixture(t, companyInfoGetDescriptor(), companyDocumentRegisterDescriptor())
	fixture.standIn.answered = aCompanyProfileAnswer()
	fixture.invoke(t, "company_info_get", map[string]string{"language": "en"})
	fixture.standIn.answered = mcp.ToolResult{StructuredContent: json.RawMessage(`{"tool":"company_document_register","result":{"documentID":"d","documentNumber":"QT-2026-0001"}}`)}
	fixture.invoke(t, "company_document_register", map[string]string{"documentType": "quote"})

	records := fixture.taskContextTheShellReads(t).Records

	if len(records) != 2 || records[0].Tool != "company_info_get" || records[1].Tool != "company_document_register" {
		t.Fatalf("the records were %+v", records)
	}
	if string(records[0].Input) != `{"language":"en"}` {
		t.Fatalf("the first record's input was %s", records[0].Input)
	}
	keptNames := []string{}
	for _, file := range records[0].Files {
		keptNames = append(keptNames, filepath.Base(file.Path))
		if !strings.HasPrefix(file.Path, filepath.Join(fixture.homePath(), "tmp", "tasks", fixture.taskRun.TaskRunID)) {
			t.Fatalf("a kept file lies outside the task directory: %s", file.Path)
		}
	}
	if strings.Join(keptNames, ",") != "company-profile.json,seal.png" {
		t.Fatalf("the files kept with the company profile were %v", keptNames)
	}
	if string(records[1].Result) != `{"documentID":"d","documentNumber":"QT-2026-0001"}` {
		t.Fatalf("the register record's result was %s", records[1].Result)
	}
}

func TestARecordToolThatFailedLeavesNoRecord(t *testing.T) {
	fixture := newTaskFixture(t, companyDocumentRegisterDescriptor())
	fixture.standIn.answered = mcp.ToolResult{IsError: true, Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"refused"}`)}}
	fixture.invoke(t, "company_document_register", map[string]string{"documentType": "quote"})

	if records := fixture.taskContextTheShellReads(t).Records; len(records) != 0 {
		t.Fatalf("a refused call was recorded: %+v", records)
	}
}

func TestTheModelCannotPointACommandAtAnotherTaskContext(t *testing.T) {
	if !security.IsWorkspaceManagedEnvironmentName(TaskContextEnvironmentName) {
		t.Fatalf("%s is not managed by the workspace, so a command's own environment could replace it", TaskContextEnvironmentName)
	}
}

func companyInfoGetDescriptor() capability.ToolDescriptor {
	descriptor := aDescriptor("company_info_get", capability.AnsweredByRecord)
	descriptor.SideEffectClass = toolcontract.ToolSideEffectRead
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"language":{"type":"string"}},"additionalProperties":false}`)
	descriptor.InputIntentSchema = descriptor.InputSchema
	descriptor.ResultContract = &capability.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"sealImage":{"type":"string"}},"additionalProperties":false}`)}
	return descriptor
}

func companyDocumentRegisterDescriptor() capability.ToolDescriptor {
	descriptor := aDescriptor("company_document_register", capability.AnsweredByRecord)
	descriptor.SideEffectClass = toolcontract.ToolSideEffectStateChange
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"documentType":{"type":"string"}},"additionalProperties":false}`)
	descriptor.InputIntentSchema = descriptor.InputSchema
	descriptor.ResultContract = &capability.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"documentID":{"type":"string"},"documentNumber":{"type":["string","null"]}},"additionalProperties":false}`)}
	return descriptor
}
