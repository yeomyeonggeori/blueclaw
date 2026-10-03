//go:build appliance && llmeval

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const (
	fileDeliveryRouteReplyAttachment = "reply attachment"
	fileDeliveryRouteRequesterDM     = "message_send DM to the requester"
	fileDeliveryRouteColleagueDM     = "message_send DM to 박예시"
	fileDeliveryRouteNone            = "no delivery"
)

type fileDeliveryRouteCase struct {
	name          string
	setupPrompts  []string
	context       []connectors.VisibleContextMessage
	prompt        string
	expectedRoute string
}

type fileDeliveryRouteOutcome struct {
	Case          string          `json:"case"`
	Run           int             `json:"run"`
	Route         string          `json:"route"`
	ExpectedRoute string          `json:"expectedRoute"`
	TaskStatus    task.TaskStatus `json:"taskStatus"`
	FirstSend     string          `json:"firstSend,omitempty"`
	Error         string          `json:"error,omitempty"`
}

func TestFileDeliveryRouteLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to run the costed file delivery route evaluation")
	}
	endpoint := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"))
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"))
	if endpoint == "" && socketPath == "" {
		t.Skip("set BLUECLAW_E2E_LLM_ENDPOINT or BLUECLAW_E2E_LLM_UNIX_SOCKET to run the file delivery route evaluation")
	}
	model := liveMemoryModel(t, endpoint, socketPath)
	runCount := fileDeliveryRouteRunCount(t)
	for _, routeCase := range fileDeliveryRouteCases() {
		t.Run(routeCase.name, func(t *testing.T) {
			outcomes := []fileDeliveryRouteOutcome{}
			for run := 1; run <= runCount; run++ {
				outcome := runFileDeliveryRouteCase(t, model, routeCase, run)
				t.Logf("run %d: %s (task %s)", outcome.Run, outcome.Route, outcome.TaskStatus)
				outcomes = append(outcomes, outcome)
			}
			writeFileDeliveryRouteEvidence(t, routeCase.name, outcomes)
			for _, outcome := range outcomes {
				if outcome.Route != outcome.ExpectedRoute {
					t.Errorf("run %d took %s, expected %s", outcome.Run, outcome.Route, outcome.ExpectedRoute)
				}
			}
		})
	}
}

func fileDeliveryRouteRunCount(t *testing.T) int {
	t.Helper()
	runs := strings.TrimSpace(os.Getenv("BLUECLAW_FILE_DELIVERY_RUNS"))
	if runs == "" {
		return 3
	}
	runCount, errorValue := strconv.Atoi(runs)
	if errorValue != nil || runCount < 1 {
		t.Fatalf("BLUECLAW_FILE_DELIVERY_RUNS must be a positive count, got %q", runs)
	}
	return runCount
}

func fileDeliveryRouteCases() []fileDeliveryRouteCase {
	agenda := []connectors.VisibleContextMessage{{
		Speaker: "샘플", SpeakerCallingName: "샘플 님", SpeakerHandle: "sample",
		Text: "다음 주 팀 회의 안건: 1) 예산 검토 2) 채용 계획 3) 워크숍 일정",
	}}
	results := []connectors.VisibleContextMessage{{
		Speaker: "샘플", SpeakerCallingName: "샘플 님", SpeakerHandle: "sample",
		Text: "3분기 영업 실적: 매출 12억 원, 전분기 대비 8% 증가, 신규 고객 34곳",
	}}
	return []fileDeliveryRouteCase{
		{
			name:          "pdf_itself",
			prompt:        "Make a one-page PDF whose only line reads 'delivery route eval', and send me the PDF file itself.",
			expectedRoute: fileDeliveryRouteReplyAttachment,
		},
		{name: "excel", context: agenda, prompt: "엑셀로 만들어서 보내줘", expectedRoute: fileDeliveryRouteReplyAttachment},
		{name: "docx", context: results, prompt: "보고서 docx로 줘", expectedRoute: fileDeliveryRouteReplyAttachment},
		{
			name:          "resend",
			setupPrompts:  []string{"다음 주 팀 회의 안건(예산 검토, 채용 계획, 워크숍 일정)을 agenda.md 파일로 정리해서 보내줘"},
			prompt:        "그 파일 다시 보내줘",
			expectedRoute: fileDeliveryRouteReplyAttachment,
		},
		{
			name:          "colleague_dm",
			setupPrompts:  []string{"이번 주 회의 요약(예산 동결, 채용 2명, 워크숍 11월)을 한 페이지 PDF로 만들어줘"},
			prompt:        "박예시 님에게 이 PDF DM으로 보내줘",
			expectedRoute: fileDeliveryRouteColleagueDM,
		},
	}
}

func runFileDeliveryRouteCase(t *testing.T, model llm.LanguageModelProvider, routeCase fileDeliveryRouteCase, run int) fileDeliveryRouteOutcome {
	t.Helper()
	outcome := fileDeliveryRouteOutcome{Case: routeCase.name, Run: run, ExpectedRoute: routeCase.expectedRoute}
	artifactDirectory := fileDeliveryRouteArtifactDirectory(t, routeCase.name, run)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	result, errorValue := RunVirtualSession(ctx, fileDeliveryRouteScenario(model, routeCase, artifactDirectory))
	preserveLiveSessionEvidence(t, artifactDirectory, result, errorValue)
	if errorValue != nil {
		outcome.Error = errorValue.Error()
	}
	if len(result.TurnResults) == 0 {
		outcome.Route = fileDeliveryRouteNone
		return outcome
	}
	measuredTurn := result.TurnResults[len(result.TurnResults)-1]
	outcome.TaskStatus = measuredTurn.TaskStatus
	outcome.Route, outcome.FirstSend = fileDeliveryRouteTaken(measuredTurn.Events)
	return outcome
}

func fileDeliveryRouteArtifactDirectory(t *testing.T, caseName string, run int) string {
	t.Helper()
	artifactRoot := firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_ARTIFACT_DIR"), filepath.Join("..", "..", ".artifacts", "file-delivery-route-live"))
	artifactDirectory := filepath.Join(artifactRoot, fmt.Sprintf("%s-%d-%d", caseName, run, time.Now().UnixNano()))
	if errorValue := os.MkdirAll(artifactDirectory, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	return artifactDirectory
}

func fileDeliveryRouteScenario(model llm.LanguageModelProvider, routeCase fileDeliveryRouteCase, artifactDirectory string) VirtualSessionScenario {
	turns := []VirtualTurn{}
	for _, setupPrompt := range routeCase.setupPrompts {
		turns = append(turns, fileDeliveryRouteTurn(setupPrompt, nil))
	}
	turns = append(turns, fileDeliveryRouteTurn(routeCase.prompt, routeCase.context))
	return VirtualSessionScenario{
		Name:                      "file_delivery_route_" + routeCase.name,
		ArtifactDirectoryPath:     artifactDirectory,
		LanguageModel:             model,
		DisableScriptedModel:      true,
		UseLooseAssertions:        true,
		SkillDirectoryPaths:       fileDeliveryRouteSkillDirectories(),
		AllowedTools:              append(agentruntime.KernelToolNames(), "message_send"),
		CapabilityToolNames:       []string{"message_send"},
		InitialToolNames:          []string{"message_send"},
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{{Name: "message_send", RequiresApproval: true}},
		Turns:                     turns,
	}
}

func fileDeliveryRouteTurn(prompt string, contextMessages []connectors.VisibleContextMessage) VirtualTurn {
	return VirtualTurn{
		Prompt:           prompt,
		ConversationType: "direct",
		ContextMessages:  contextMessages,
		RouterTaskShape:  agentcontract.TaskShapeMaintenanceTask,
		AllowedTaskStatuses: []task.TaskStatus{
			task.TaskStatusCompleted, task.TaskStatusWaitingApproval, task.TaskStatusWaitingUserInput, task.TaskStatusFailed,
		},
	}
}

func fileDeliveryRouteSkillDirectories() []string {
	directories := []string{}
	for _, skillName := range []string{"office", "direct-message", "messages"} {
		if directory := findScenarioSkillDirectory(skillName); directory != "" {
			directories = append(directories, directory)
		}
	}
	return directories
}

func fileDeliveryRouteTaken(events []task.TaskEvent) (string, string) {
	messageSendRequested := agentcontract.ToolTaskEventName("message_send", agentcontract.ToolTaskEventRequestedSuffix)
	fileDeliverRequested := agentcontract.ToolTaskEventName("file_deliver", agentcontract.ToolTaskEventRequestedSuffix)
	for _, event := range events {
		switch event.Name {
		case fileDeliverRequested:
			return fileDeliveryRouteReplyAttachment, ""
		case messageSendRequested:
			return messageSendRoute(event.Body), event.Body
		}
	}
	return fileDeliveryRouteNone, ""
}

func messageSendRoute(requestedBody string) string {
	var requested struct {
		Input struct {
			TargetType  string   `json:"targetType"`
			PersonHint  string   `json:"personHint"`
			PersonHints []string `json:"personHints"`
		} `json:"input"`
	}
	_ = json.Unmarshal([]byte(requestedBody), &requested)
	input := requested.Input
	if input.TargetType != "directMessage" {
		return "message_send " + input.TargetType
	}
	recipients := append([]string{input.PersonHint}, input.PersonHints...)
	if strings.Contains(strings.Join(recipients, " "), "예시") {
		return fileDeliveryRouteColleagueDM
	}
	if strings.TrimSpace(strings.Join(recipients, "")) == "" || strings.Contains(strings.Join(recipients, " "), "샘플") {
		return fileDeliveryRouteRequesterDM
	}
	return "message_send DM to " + strings.Join(recipients, ", ")
}

func writeFileDeliveryRouteEvidence(t *testing.T, caseName string, outcomes []fileDeliveryRouteOutcome) {
	t.Helper()
	document, errorValue := json.MarshalIndent(outcomes, "", "  ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	artifactRoot := firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_ARTIFACT_DIR"), filepath.Join("..", "..", ".artifacts", "file-delivery-route-live"))
	evidencePath := filepath.Join(artifactRoot, fmt.Sprintf("outcomes-%s-%d.json", caseName, time.Now().UnixNano()))
	if errorValue := os.WriteFile(evidencePath, document, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Logf("file delivery route evidence: %s", evidencePath)
}
