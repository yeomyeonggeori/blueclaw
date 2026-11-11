//go:build !nobundledharness

package agentruntime

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type requesterSeeingHarness struct {
	seen model.RequestContext
}

func (harness *requesterSeeingHarness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	harness.seen = model.RequestContextFromContext(ctx)
	return agentcontract.AgentTurnResult{TaskRun: agentcontract.TaskRun{TaskRunID: request.ExistingTaskRunID}}, nil
}

func TestEverythingATaskLaunchRunsKnowsWhoAskedForTheTask(t *testing.T) {
	taskRuns := task.NewTaskRunService(task.NewTaskEventService())
	harness := &requesterSeeingHarness{}
	request := TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		RequesterPersonID:         "person-1",
		RequesterEmail:            "park.sample@example.com",
		ProfileName:               "default",
		Platform:                  "test",
		ConversationID:            "channel-1",
		Prompt:                    "발표자료 만들어줘",
		HistoryProvider:           staticHistoryProvider{},
		PersonAccess:              policy.PersonAccess{PersonID: "person-1"},
		AccessibleConversationIDs: []string{"channel-1"},
	}

	if _, errorValue := routedTaskLauncher(harness, taskRuns, NewToolCatalogBuilder(), staticRuntimeLanguageModel{content: runtimeFinishMessage("done")}).Launch(context.Background(), request); errorValue != nil {
		t.Fatalf("launch: %v", errorValue)
	}

	if harness.seen.RequesterEmail != "park.sample@example.com" || harness.seen.RequesterPersonID != "person-1" || harness.seen.ConversationID != "channel-1" {
		t.Fatalf("a harness routes and classifies before and after its turn, so the launch must already name the requester; it saw %+v", harness.seen)
	}
}
