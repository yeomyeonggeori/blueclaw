package integration

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/httpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueclaw/internal/tui"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func terminalOperatorDaemon(t *testing.T) (*httptest.Server, *task.TaskRunService) {
	t.Helper()
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	router := httpserver.NewRouter(httpserver.RouterDependencies{
		TaskMonitorHandler: adminapi.TaskMonitorHandler{
			TaskRunService:   taskRunService,
			TaskStepService:  taskStepService,
			TaskEventService: taskEventService,
		},
		HarnessStatusHandler: adminapi.HarnessStatusHandler{Status: adminapi.HarnessStatus{
			Name:                    "claude-code",
			AgentCommandPath:        "/usr/local/bin/claude",
			RunsAsRequesterIdentity: true,
			ToolCatalogURL:          "http://127.0.0.1:8080/harness/tool-catalog",
		}},
		TaskApprovalHandler: adminapi.TaskApprovalHandler{
			TaskRunService: taskRunService,
			TaskLauncher:   agentruntime.NewTaskLauncher(nil, taskRunService, agentruntime.NewToolCatalogBuilder()),
		},
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, taskRunService
}

func TestTerminalOperatorReadsTheRealAdminSurfaceThroughTheTerminalClient(t *testing.T) {
	server, taskRunService := terminalOperatorDaemon(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "내일 회의 캘린더에서 지워줘")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "tool.event_delete.requested", `{"tool":"event_delete"}`)
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "tool.event_delete.result", `{"tool":"event_delete"}`)
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventApprovalPendingCall, `{"toolName":"event_delete","confirmation":"내일 회의를 지울까요?"}`)
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, task.TaskStatusWaitingApproval, "내일 회의를 지울까요?"); errorValue != nil {
		t.Fatalf("expected the run to reach the approval gate: %v", errorValue)
	}

	terminalClient := tui.NewClient(server.URL, server.Client())
	taskRuns, errorValue := terminalClient.ListTaskRuns(context.Background())
	if errorValue != nil {
		t.Fatalf("expected the terminal to list task runs from the real admin surface: %v", errorValue)
	}
	if len(taskRuns) != 1 || taskRuns[0].TaskRunID != taskRun.TaskRunID || taskRuns[0].Prompt != "내일 회의 캘린더에서 지워줘" {
		t.Fatalf("expected the real task run to decode into the terminal's shape, got %+v", taskRuns)
	}
	waitingApproval := tui.FilterWaitingApproval(taskRuns)
	if len(waitingApproval) != 1 {
		t.Fatalf("expected the run to appear in the approval queue, got %+v", waitingApproval)
	}

	detail, errorValue := terminalClient.GetTaskRunDetail(context.Background(), taskRun.TaskRunID)
	if errorValue != nil {
		t.Fatalf("expected the terminal to read the event ledger: %v", errorValue)
	}
	if len(detail.TaskEvents) < 3 {
		t.Fatalf("expected the ledger to reach the terminal, got %+v", detail.TaskEvents)
	}
	question, _ := tui.LatestApprovalQuestion(detail.TaskEvents)
	if question != "내일 회의를 지울까요?" {
		t.Fatalf("expected the approval question to reach the operator, got %q", question)
	}
	timeline := tui.BuildTimeline(detail.TaskEvents)
	if len(timeline) == 0 {
		t.Fatalf("expected a rendered timeline, got %+v", timeline)
	}
}

func TestTerminalOperatorApprovalReachesTheRealGate(t *testing.T) {
	server, taskRunService := terminalOperatorDaemon(t)
	runningTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "캘린더 정리")
	terminalClient := tui.NewClient(server.URL, server.Client())

	if _, errorValue := terminalClient.SubmitApproval(context.Background(), runningTaskRun.TaskRunID, "confirm"); errorValue == nil {
		t.Fatal("expected the real gate to refuse approving a run that is not waiting for approval")
	}
	if _, errorValue := terminalClient.SubmitApproval(context.Background(), runningTaskRun.TaskRunID, "looks fine"); errorValue == nil {
		t.Fatal("expected the real gate to refuse a decision outside the allowed set")
	}
	if _, errorValue := terminalClient.SubmitApproval(context.Background(), "missing", "confirm"); errorValue == nil {
		t.Fatal("expected the real gate to refuse an unknown task run")
	}
}

func TestTerminalOperatorSeesTheHarnessTheDaemonIsActuallyRunning(t *testing.T) {
	server, _ := terminalOperatorDaemon(t)
	terminalClient := tui.NewClient(server.URL, server.Client())

	harnessStatus, errorValue := terminalClient.GetHarnessStatus(context.Background())
	if errorValue != nil {
		t.Fatalf("expected the terminal to read the running harness: %v", errorValue)
	}
	harnessInfo := tui.HarnessInfoFromStatus(harnessStatus)
	if !harnessInfo.IsLiveReport || !harnessInfo.IsKnown {
		t.Fatalf("expected a live harness report, got %+v", harnessInfo)
	}
	if harnessInfo.Name != "claude-code" {
		t.Fatalf("expected the running harness name, got %q", harnessInfo.Name)
	}
	if !harnessInfo.RunsAsRequesterIdentity {
		t.Fatal("expected the operator to be told whether the harness runs inside the requester's identity")
	}
}
