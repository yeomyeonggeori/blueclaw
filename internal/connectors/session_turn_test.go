package connectors

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

type approvingTurnRouter struct{}

func (router approvingTurnRouter) Plan(ctx context.Context, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	return router.PlanObserved(ctx, request, nil)
}

func (approvingTurnRouter) PlanObserved(context.Context, agentcontract.AgentRequest, *agentcontract.IntakeCallLedger) (agentcontract.TurnDecision, error) {
	approval := agentcontract.ApprovalSignalApprove
	return agentcontract.TurnDecision{Route: agentcontract.TurnRouteContinueTask, Approval: &approval}, nil
}

func TestASessionTurnLeavesARunWaitingOnApprovalToTheSessionThatAsked(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	connectorRuntime, _ := connectorRuntimeForHarness(t, harness, harness, harness, approvingTurnRouter{}, taskRunService, testLanguageModel{reply: "stub"})
	running := seedAbandonedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "박예시한테 DM 보내줘")
	if _, errorValue := connectorRuntime.taskRunService.PauseTaskRun(running.TaskRunID, task.TaskStatusWaitingApproval, "박예시에게 보낼까요?"); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-yes")
	event.Prompt = "응 보내"
	isThread := false
	event.IsThread = &isThread
	sessionTurn := connectorRuntime.OpenSessionTurn(context.Background(), event, "person-1", func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		t.Fatal("the session turn answered a message whose approval the session asks for itself")
		return "", nil
	})

	launchRequest, isAnswered, errorValue := sessionTurn.PrepareLaunch(context.Background(), agentruntime.TaskLaunchRequest{Prompt: event.Prompt})

	if errorValue != nil || isAnswered {
		t.Fatalf("the turn ended in settlement: answered=%v error=%v", isAnswered, errorValue)
	}
	if launchRequest.IsApprovalContinuation || launchRequest.ExistingTaskRunID != "" {
		t.Fatalf("the message continued %q as an approval, which would carry out a call the session is still asking about", launchRequest.ExistingTaskRunID)
	}
}

const relayPictureAddress = "http://127.0.0.1:3000/media/0f1e2d3c.png"

func TestASessionTurnLaunchesWithItsPictureInTheRequestersInbox(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	adapter.inputAttachmentImportResult = InputAttachmentImportResult{
		InputParts: []agentcontract.AgentPart{{
			Type:   agentcontract.AgentPartTypeImage,
			Image:  &agentcontract.AgentImagePart{Path: "/workspace/private/people/person-1/inbox/test/dm/0f1e2d3c.png", MimeType: "image/png", DataBase64: "cGljdHVyZQ=="},
			Source: agentcontract.AgentPartSource{Platform: "test", MessageID: "message-picture"},
		}},
		InputAttachments: []InputAttachment{{
			Platform:    "test",
			URL:         relayPictureAddress,
			MessageID:   "message-picture",
			Filename:    "0f1e2d3c.png",
			ContentType: "image/png",
			Path:        "/workspace/private/people/person-1/inbox/test/dm/0f1e2d3c.png",
			IsAvailable: true,
		}},
	}
	event := testInboundEvent("message-picture")
	event.ConversationID = "dm:person-1"
	event.Context.ConversationType = "dm"
	event.Context.InputAttachments = []InputAttachment{{Platform: "test", URL: relayPictureAddress, MessageID: "message-picture", ContentType: "image/png"}}
	sessionTurn := connectorRuntime.OpenSessionTurn(context.Background(), event, "person-1", adapter.SendReply)
	defer sessionTurn.End()

	launchRequest, isAnswered, errorValue := sessionTurn.PrepareLaunch(context.Background(), agentruntime.TaskLaunchRequest{Prompt: event.Prompt})

	if errorValue != nil || isAnswered {
		t.Fatalf("the turn ended in settlement: answered=%v error=%v", isAnswered, errorValue)
	}
	if len(adapter.inputAttachmentImportRequests) != 1 {
		t.Fatalf("the adapter was asked to import %d times, expected once", len(adapter.inputAttachmentImportRequests))
	}
	if target := adapter.inputAttachmentImportRequests[0].TargetDirectoryPath; !strings.HasSuffix(target, "/inbox/test/dm") {
		t.Fatalf("the picture was imported into %q, expected the requester's dm inbox", target)
	}
	if current := launchRequest.VisibleContext.CurrentMaterials; len(current) != 1 || current[0].Path != "~/inbox/test/dm/0f1e2d3c.png" {
		t.Fatalf("the turn sees %+v as the message's attachments, expected the picture at its inbox path", current)
	}
	if len(launchRequest.InputParts) != 1 || launchRequest.InputParts[0].Image == nil {
		t.Fatalf("the turn was given parts %+v, expected the picture", launchRequest.InputParts)
	}
	if launchRequest.AttachmentMaterialResolver == nil {
		t.Fatal("no resolver came with the launch, so the turn cannot read an attachment the conversation shows")
	}
	material, errorValue := launchRequest.AttachmentMaterialResolver.ResolveAttachmentMaterial(context.Background(), relayPictureAddress)
	if errorValue != nil {
		t.Fatalf("the picture did not resolve by the address the message showed: %v", errorValue)
	}
	if material.Path != "~/inbox/test/dm/0f1e2d3c.png" {
		t.Fatalf("the picture resolved to %q, expected its inbox path", material.Path)
	}
}

func TestASessionTurnOnAPlatformNobodyServesLaunchesAsItCame(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	event := testInboundEvent("message-picture")
	event.Platform = "elsewhere"
	event.Context.InputAttachments = []InputAttachment{{Platform: "elsewhere", URL: relayPictureAddress}}
	sessionTurn := connectorRuntime.OpenSessionTurn(context.Background(), event, "person-1", adapter.SendReply)
	defer sessionTurn.End()

	launchRequest, isAnswered, errorValue := sessionTurn.PrepareLaunch(context.Background(), agentruntime.TaskLaunchRequest{Prompt: event.Prompt})

	if errorValue != nil || isAnswered {
		t.Fatalf("the turn ended in settlement: answered=%v error=%v", isAnswered, errorValue)
	}
	if launchRequest.AttachmentMaterialResolver != nil || len(launchRequest.InputParts) != 0 {
		t.Fatalf("a platform nothing serves was prepared: resolver %+v parts %+v", launchRequest.AttachmentMaterialResolver, launchRequest.InputParts)
	}
	if len(adapter.inputAttachmentImportRequests) != 0 || len(adapter.progressStarts) != 0 {
		t.Fatalf("another platform's adapter was used: imports %+v progress %+v", adapter.inputAttachmentImportRequests, adapter.progressStarts)
	}
}

func TestASessionTurnShowsTypingOnTheConnectorRuleUntilItEnds(t *testing.T) {
	cases := []struct {
		name            string
		event           PlatformInboundEvent
		startsAtTheOpen int
	}{
		{name: "direct message", event: testInboundEvent("message-1"), startsAtTheOpen: 1},
		{name: "channel mention", event: mentionedInChannel(testChannelInboundEvent("message-1")), startsAtTheOpen: 1},
		{name: "channel chatter", event: testChannelInboundEvent("message-1"), startsAtTheOpen: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)

			sessionTurn := connectorRuntime.OpenSessionTurn(context.Background(), testCase.event, "person-1", adapter.SendReply)
			if len(adapter.progressStarts) != testCase.startsAtTheOpen {
				t.Fatalf("typing started %d times before the gate, expected %d", len(adapter.progressStarts), testCase.startsAtTheOpen)
			}
			if _, _, errorValue := sessionTurn.PrepareLaunch(context.Background(), agentruntime.TaskLaunchRequest{Prompt: testCase.event.Prompt}); errorValue != nil {
				t.Fatalf("prepare launch: %v", errorValue)
			}
			if len(adapter.progressStarts) != 1 || adapter.progressStarts[0].ReplyTargetID != testCase.event.ReplyTargetID {
				t.Fatalf("typing started at %+v by launch, expected once at %q", adapter.progressStarts, testCase.event.ReplyTargetID)
			}
			sessionTurn.End()
			if len(adapter.progressStops) != 1 {
				t.Fatalf("typing stopped %d times once the turn ended, expected once", len(adapter.progressStops))
			}
		})
	}
}

func TestASessionTurnTheGateTurnsAwayNeverShowsTyping(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)

	connectorRuntime.OpenSessionTurn(context.Background(), testChannelInboundEvent("message-1"), "person-1", adapter.SendReply).End()

	if len(adapter.progressStarts) != 0 || len(adapter.progressStops) != 0 {
		t.Fatalf("channel chatter the gate never let in showed typing: starts %+v stops %+v", adapter.progressStarts, adapter.progressStops)
	}
}

func mentionedInChannel(event PlatformInboundEvent) PlatformInboundEvent {
	event.Context.Addressing.BotMentioned = true
	return event
}
