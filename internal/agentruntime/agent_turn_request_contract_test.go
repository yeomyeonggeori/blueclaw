package agentruntime

import (
	"context"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

func TestLaunchedAgentTurnRequestCarriesHostAssembledContext(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	memoryStores := memorytest.Open(t)
	memorytest.Remember(t, memoryStores, memory.PersonScope("person-1"), "The user leads the quarterly launch project.")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStores(memoryStores, nil)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory_search"},
	}, nil)

	_, errorValue := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:               TaskLaunchSourceConnector,
		SourceReference:      "mattermost:post-1",
		RequesterPersonID:    "person-1",
		RequesterName:        "김샘플",
		RequesterEmail:       "sample@example.com",
		RequesterCallingName: "샘플 님",
		RequesterHandle:      "sample",
		ProfileName:          "default",
		Platform:             "mattermost",
		ConversationID:       "channel-1",
		Prompt:               "이 파일 요약해줘",
		ResponseLanguage:     "ko",
		VisibleContext: agentcontract.VisibleContext{
			Messages: []agentcontract.VisibleContextMessage{
				{Speaker: "user", Text: "지난 분기 실적 자료 공유합니다"},
			},
			Materials: []agentcontract.VisibleContextMaterial{
				{Filename: "quarterly.pdf", Path: "/workspace/private/people/person-1/quarterly.pdf", IsAvailable: true},
			},
		},
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}

	turnRequest := harness.LastTurnRequest()
	if turnRequest.RequesterPersonID != "person-1" || turnRequest.RequesterEmail != "sample@example.com" || turnRequest.RequesterName != "김샘플" {
		t.Fatalf("expected requester identity on the turn request, got %+v", turnRequest)
	}
	if turnRequest.Prompt != "이 파일 요약해줘" || turnRequest.ProfileName != "default" || turnRequest.ConversationID != "channel-1" {
		t.Fatalf("expected launch routing fields on the turn request, got %+v", turnRequest)
	}
	if len(turnRequest.VisibleContext.Messages) != 1 || turnRequest.VisibleContext.Messages[0].Text != "지난 분기 실적 자료 공유합니다" {
		t.Fatalf("expected visible context messages on the turn request, got %+v", turnRequest.VisibleContext)
	}
	if len(turnRequest.VisibleContext.Materials) == 0 || turnRequest.VisibleContext.Materials[0].Filename != "quarterly.pdf" {
		t.Fatalf("expected attachment materials on the turn request, got %+v", turnRequest.VisibleContext.Materials)
	}
	if len(turnRequest.MemoryFacts) != 1 {
		t.Fatalf("expected the recalled memory on the turn request, got %+v", turnRequest.MemoryFacts)
	}
	if turnRequest.MemoryFacts[0].ScopeType != memory.ScopePerson ||
		!strings.Contains(turnRequest.MemoryFacts[0].Content, "The user leads the quarterly launch project.") {
		t.Fatalf("expected the memory from the person's own file, got %+v", turnRequest.MemoryFacts)
	}
	if turnRequest.ToolSet == nil || !containsString(turnRequest.ToolSet.ListToolNames(), "memory_search") {
		t.Fatalf("expected the launch tool set on the turn request, got %+v", turnRequest.ToolSet)
	}
	if !containsString(turnRequest.PinnedToolNames, "memory_search") {
		t.Fatalf("expected registered memory_search to remain callable in the turn working set, got %+v", turnRequest.PinnedToolNames)
	}
	if !containsString(turnRequest.RequesterCircles, "member") {
		t.Fatalf("expected resolved requester circles on the turn request, got %+v", turnRequest.RequesterCircles)
	}
}
