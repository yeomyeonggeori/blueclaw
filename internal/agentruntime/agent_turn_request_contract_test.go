package agentruntime

import (
	"context"
	"github.com/yeomyeonggeori/bluememo"
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
	memoryStore := seededMemoryStore(t, "person-1", "The user leads the quarterly launch project.")
	if errorValue := memoryStore.Profiles.SaveProfile(context.Background(), bluememo.Profile{PersonID: "person-1", IdentityLines: []string{"The user prefers terse release notes."}}); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStore(memoryStore, nil, nil)
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
	if len(turnRequest.MemoryFacts) != 2 {
		t.Fatalf("expected the profile line and the recalled fact on the turn request, got %+v", turnRequest.MemoryFacts)
	}
	if turnRequest.MemoryFacts[0].SourceKind != "profile" ||
		!strings.Contains(turnRequest.MemoryFacts[0].Content, "The user prefers terse release notes.") {
		t.Fatalf("expected the profile first, got %+v", turnRequest.MemoryFacts)
	}
	if turnRequest.MemoryFacts[1].Content != "The user leads the quarterly launch project." {
		t.Fatalf("expected the recalled fact after the profile, got %+v", turnRequest.MemoryFacts)
	}
	if turnRequest.ToolSet == nil || !containsString(turnRequest.ToolSet.ListToolNames(), "memory_search") {
		t.Fatalf("expected the launch tool set on the turn request, got %+v", turnRequest.ToolSet)
	}
	if !containsString(turnRequest.RequesterCircles, "member") {
		t.Fatalf("expected resolved requester circles on the turn request, got %+v", turnRequest.RequesterCircles)
	}
}
