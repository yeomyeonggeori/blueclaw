package agentruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestMorningBriefingIsEmptyRequiresCompleteTaskCount(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		tasks  string
		expect string
	}{
		{name: "missing count", tasks: `{"tasks":[],"unfinishedCount":0}`, expect: "complete unfinished task count"},
		{name: "missing unfinished count", tasks: `{"tasks":[],"count":0}`, expect: "complete unfinished task count"},
		{name: "missing task array", tasks: `{"count":0,"unfinishedCount":0}`, expect: "complete unfinished task count"},
		{name: "count mismatch", tasks: `{"tasks":[{}],"count":0,"unfinishedCount":0}`, expect: "complete unfinished task count"},
		{name: "negative unfinished count", tasks: `{"tasks":[],"count":0,"unfinishedCount":-1}`, expect: "complete unfinished task count"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			toolSet := morningBriefingTestToolSet(testCase.tasks, `{"events":[]}`, nil)
			_, errorValue := morningBriefingIsEmpty(context.Background(), toolSet, sampleBriefingRequester, "Asia/Seoul", time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC))
			if errorValue == nil || !strings.Contains(errorValue.Error(), testCase.expect) {
				t.Fatalf("expected malformed task response error, got %v", errorValue)
			}
		})
	}
}

func TestMorningBriefingIsEmptyRequiresSuccessfulReads(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		taskResult  toolcontract.ToolResult
		eventResult toolcontract.ToolResult
	}{
		{name: "task failure", taskResult: toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "task_list", "failed")},
		{name: "event failure", taskResult: toolcontract.ToolSuccessData("tasks", json.RawMessage(`{"tasks":[],"count":0,"unfinishedCount":0}`)), eventResult: toolcontract.ToolFailureResult(toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "event_list", "denied")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			toolSet := morningBriefingTestToolSet(`{"tasks":[],"count":0,"unfinishedCount":0}`, `{"events":[]}`, func(name string) toolcontract.ToolResult {
				if name == "task_list" && testCase.taskResult.Failure != nil {
					return testCase.taskResult
				}
				if name == "event_list" && testCase.eventResult.Failure != nil {
					return testCase.eventResult
				}
				return toolcontract.ToolSuccessData("ok", json.RawMessage(`{"tasks":[],"count":0,"unfinishedCount":0}`))
			})
			_, errorValue := morningBriefingIsEmpty(context.Background(), toolSet, sampleBriefingRequester, "Asia/Seoul", time.Now())
			if errorValue == nil {
				t.Fatal("expected failed tool read to prevent skipping")
			}
		})
	}
}

func TestMorningBriefingIsEmptyKeepsNonEmptyWorkAndCalendar(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		tasks       string
		events      string
		expectEmpty bool
	}{
		{name: "no unfinished work and no events", tasks: `{"tasks":[],"count":0,"unfinishedCount":0}`, events: `{"events":[]}`, expectEmpty: true},
		{name: "unfinished work beyond list limit", tasks: `{"tasks":[],"count":0,"unfinishedCount":2}`, events: `{"events":[]}`},
		{name: "calendar event", tasks: `{"tasks":[],"count":0,"unfinishedCount":0}`, events: `{"events":[{}]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			toolSet := morningBriefingTestToolSet(testCase.tasks, testCase.events, nil)
			isEmpty, errorValue := morningBriefingIsEmpty(context.Background(), toolSet, sampleBriefingRequester, "Asia/Seoul", time.Now())
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if isEmpty != testCase.expectEmpty {
				t.Fatalf("expected empty=%v, got %v", testCase.expectEmpty, isEmpty)
			}
		})
	}
}

func TestMorningBriefingPreflightScopesBothReadsToRequester(t *testing.T) {
	inputs := []json.RawMessage{}
	toolSet := morningBriefingTestToolSet(`{"tasks":[],"count":0,"unfinishedCount":0}`, `{"events":[]}`, func(name string) toolcontract.ToolResult {
		return toolcontract.ToolSuccessData(name, json.RawMessage(`{}`))
	})
	// Re-register handlers so the test can inspect the exact wire inputs while
	// retaining the typed read definitions used by the preflight.
	toolSet.AllowTestReplacement()
	for _, toolName := range []string{"task_list", "event_list"} {
		name := toolName
		_ = toolSet.RegisterTool(toolcontract.ToolDefinition{
			Name:            name,
			Visibility:      toolcontract.ToolVisibilityModel,
			SideEffectClass: toolcontract.ToolSideEffectRead,
			ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{}`)},
		}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			inputs = append(inputs, append(json.RawMessage{}, invocation.Input...))
			if name == "task_list" {
				return toolcontract.ToolSuccessData("tasks", json.RawMessage(`{"tasks":[],"count":0,"unfinishedCount":0}`)), nil
			}
			return toolcontract.ToolSuccessData("events", json.RawMessage(`{"events":[]}`)), nil
		})
	}
	if isEmpty, errorValue := morningBriefingIsEmpty(context.Background(), toolSet, sampleBriefingRequester, "Asia/Seoul", time.Now()); errorValue != nil || !isEmpty {
		t.Fatalf("expected empty preflight, got empty=%v error=%v", isEmpty, errorValue)
	}
	if len(inputs) != 2 {
		t.Fatalf("expected task and event reads, got %d", len(inputs))
	}
	for _, input := range inputs {
		var document struct {
			PersonHints []string `json:"personHints"`
		}
		if errorValue := json.Unmarshal(input, &document); errorValue != nil {
			t.Fatal(errorValue)
		}
		isTaskRead := len(document.PersonHints) > 0
		if isTaskRead && (len(document.PersonHints) != 1 || document.PersonHints[0] != "sample@example.test") {
			t.Fatalf("expected the task read scoped to the requester, got %s", input)
		}
	}
}

func TestMorningBriefingCalendarInputUsesLocalDayAcrossDST(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		timeZone  string
		reference time.Time
		start     string
		end       string
	}{
		{name: "local midnight", timeZone: "Asia/Seoul", reference: time.Date(2026, 6, 15, 23, 30, 0, 0, time.UTC), start: "2026-06-16T00:00:00+09:00", end: "2026-06-17T00:00:00+09:00"},
		{name: "DST spring day", timeZone: "America/New_York", reference: time.Date(2026, 3, 8, 16, 0, 0, 0, time.UTC), start: "2026-03-08T00:00:00-05:00", end: "2026-03-09T00:00:00-04:00"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input, errorValue := morningBriefingCalendarInput(testCase.timeZone, testCase.reference)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			var calendarInput struct {
				StartsAt string `json:"startsAt"`
				EndsAt   string `json:"endsAt"`
			}
			if errorValue := json.Unmarshal(input, &calendarInput); errorValue != nil {
				t.Fatal(errorValue)
			}
			if calendarInput.StartsAt != testCase.start || calendarInput.EndsAt != testCase.end {
				t.Fatalf("expected %s through %s, got %s through %s", testCase.start, testCase.end, calendarInput.StartsAt, calendarInput.EndsAt)
			}
		})
	}
}

func TestMorningBriefingPreflightRequiresRequesterEmail(t *testing.T) {
	toolSet := morningBriefingTestToolSet(`{"tasks":[],"count":0,"unfinishedCount":0}`, `{"events":[]}`, nil)
	if _, errorValue := morningBriefingIsEmpty(context.Background(), toolSet, morningBriefingRequester{personID: "person-1"}, "Asia/Seoul", time.Now()); errorValue == nil {
		t.Fatal("expected a missing requester email to fail closed")
	}
}

func TestMorningBriefingCalendarInputRejectsMissingOrUnknownTimezone(t *testing.T) {
	for _, timeZone := range []string{"", "Mars/Olympus"} {
		if _, errorValue := morningBriefingCalendarInput(timeZone, time.Now()); errorValue == nil {
			t.Fatalf("expected timezone %q to fail", timeZone)
		}
	}
}

var sampleBriefingRequester = morningBriefingRequester{personID: "person-1", email: "sample@example.test"}

func TestMorningBriefingIsEmptyCountsWhatIsOnTheRequestersScheduleToday(t *testing.T) {
	const noTasks = `{"tasks":[],"count":0,"unfinishedCount":0}`
	ownEvent := `{"source":"event","participants":[{"personID":"person-1","email":"sample@example.test"}]}`
	otherEvent := `{"source":"event","participants":[{"personID":"person-2","email":"other@example.test"}]}`
	otherLeave := `{"source":"leave","participants":[{"personID":"person-2","email":"other@example.test"}]}`
	companyEvent := `{"source":"event","participants":[]}`
	for _, testCase := range []struct {
		name        string
		calendar    []string
		expectEmpty bool
	}{
		{name: "nothing on the calendar", calendar: nil, expectEmpty: true},
		{name: "only another person's event and leave", calendar: []string{otherEvent, otherLeave}, expectEmpty: true},
		{name: "the requester's own event", calendar: []string{otherEvent, ownEvent}},
		{name: "an event open to the whole company", calendar: []string{companyEvent}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			toolSet := morningBriefingHostToolSet(noTasks, testCase.calendar)
			isEmpty, errorValue := morningBriefingIsEmpty(context.Background(), toolSet, sampleBriefingRequester, "Asia/Seoul", time.Now())
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if isEmpty != testCase.expectEmpty {
				t.Fatalf("expected empty=%v, got %v", testCase.expectEmpty, isEmpty)
			}
		})
	}
}

func morningBriefingHostToolSet(tasks string, calendar []string) *toolcontract.ToolSet {
	toolSet := toolcontract.NewToolSet([]string{"task_list", "event_list"})
	for _, toolName := range []string{"task_list", "event_list"} {
		_ = toolSet.RegisterTool(toolcontract.ToolDefinition{
			Name:            toolName,
			Visibility:      toolcontract.ToolVisibilityModel,
			SideEffectClass: toolcontract.ToolSideEffectRead,
			ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{}`)},
		}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			if invocation.ToolName == "task_list" {
				return toolcontract.ToolSuccessData("tasks", json.RawMessage(tasks)), nil
			}
			return toolcontract.ToolSuccessData("events", hostEventList(invocation.Input, calendar)), nil
		})
	}
	return toolSet
}

func hostEventList(input json.RawMessage, calendar []string) json.RawMessage {
	var request struct {
		PersonHints []string `json:"personHints"`
	}
	_ = json.Unmarshal(input, &request)
	events := []string{}
	for _, entry := range calendar {
		isNamed := len(request.PersonHints) == 0 || strings.Contains(entry, request.PersonHints[0])
		if !isNamed {
			continue
		}
		events = append(events, entry)
	}
	return json.RawMessage(`{"events":[` + strings.Join(events, ",") + `]}`)
}

func morningBriefingTestToolSet(tasks string, events string, replacement func(string) toolcontract.ToolResult) *toolcontract.ToolSet {
	toolSet := toolcontract.NewToolSet([]string{"task_list", "event_list"})
	for _, toolName := range []string{"task_list", "event_list"} {
		definition := toolcontract.ToolDefinition{
			Name:            toolName,
			Visibility:      toolcontract.ToolVisibilityModel,
			SideEffectClass: toolcontract.ToolSideEffectRead,
			ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{}`)},
		}
		_ = toolSet.RegisterTool(definition, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			if replacement != nil {
				return replacement(invocation.ToolName), nil
			}
			if invocation.ToolName == "task_list" {
				return toolcontract.ToolSuccessData("tasks", json.RawMessage(tasks)), nil
			}
			return toolcontract.ToolSuccessData("events", json.RawMessage(events)), nil
		})
	}
	return toolSet
}

func TestMorningBriefingScheduleIdentificationRemainsNarrow(t *testing.T) {
	schedule := task.Schedule{CreatorPersonID: "person-1", ScheduleID: task.MorningBriefingScheduleID("person-1")}
	if !task.IsMorningBriefing(schedule) {
		t.Fatal("expected the managed morning briefing schedule to be identified")
	}
	schedule.ScheduleID = "schedule-other"
	if task.IsMorningBriefing(schedule) {
		t.Fatal("expected an ordinary schedule to bypass the managed preflight")
	}
}

func TestScheduleRunnerSkipsEmptyManagedBriefingWithoutLaunching(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{"default": {"task_list", "event_list"}}, nil)
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{
		Endpoint:   "http://capability.local",
		HTTPClient: emptyBriefingCapabilityClient{},
	}, []CapabilityToolDescriptor{
		{Name: "task_list", InputSchema: json.RawMessage(`{"type":"object","properties":{"personHints":{"type":"array","items":{"type":"string"}},"everyWeek":{"type":"boolean"},"limit":{"type":"integer"}},"additionalProperties":false}`), ResultContract: &CapabilityToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"tasks":{"type":"array"},"count":{"type":"integer"},"unfinishedCount":{"type":"integer"}},"required":["tasks","count","unfinishedCount"],"additionalProperties":false}`)}},
		{Name: "event_list", InputSchema: json.RawMessage(`{"type":"object","properties":{"personHints":{"type":"array","items":{"type":"string"}},"startsAt":{"type":"string"},"endsAt":{"type":"string"},"limit":{"type":"integer"}},"additionalProperties":false}`), ResultContract: &CapabilityToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"events":{"type":"array"}},"required":["events"],"additionalProperties":false}`)}},
	})
	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseRequesterEmailResolver(briefingRequesterEmailResolver{})
	runAt := time.Date(2026, 6, 15, 23, 0, 0, 0, time.UTC)
	schedule := task.Schedule{
		ScheduleID:      task.MorningBriefingScheduleID("person-1"),
		CreatorPersonID: "person-1",
		Kind:            task.ScheduleKindCron,
		CronExpression:  "0 8 * * *",
		TimeZone:        "Asia/Seoul",
		NextRunAt:       &runAt,
	}
	result, errorValue := NewScheduleRunner(taskLauncher).RunIfDue(context.Background(), ScheduleRunRequest{
		Schedule:      schedule,
		ReferenceTime: runAt,
		PersonAccess:  policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.DidRun {
		t.Fatal("expected empty managed briefing to skip launch")
	}
	if result.Schedule.NextRunAt == nil || !result.Schedule.NextRunAt.After(runAt) {
		t.Fatalf("expected schedule to advance, got %+v", result.Schedule)
	}
	if len(taskRunService.ListTaskRun()) != 0 {
		t.Fatal("expected skip to create no task run")
	}
}

type emptyBriefingCapabilityClient struct{}

func (emptyBriefingCapabilityClient) Do(request *http.Request) (*http.Response, error) {
	result := `{"tasks":[],"count":0,"unfinishedCount":0}`
	if strings.HasSuffix(request.URL.Path, "/event_list/invoke") {
		result = `{"events":[]}`
	}
	body := `{"provider":"test","selectedBackend":"device","toolName":"` + strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/tools/"), "/invoke") + `","outcome":"succeeded","status":"ok","result":` + result + `}`
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
}

type briefingRequesterEmailResolver struct{}

func (briefingRequesterEmailResolver) ResolvePersonPrimaryEmail(string) string {
	return "sample@example.test"
}
