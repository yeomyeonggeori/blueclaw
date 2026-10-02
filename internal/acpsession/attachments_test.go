package acpsession

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const pictureAddress = "http://127.0.0.1:3000/media/0f1e2d3c.png"
const inboxPicturePath = "/workspace/private/people/person-sample/inbox/buzz/conversation-1/0f1e2d3c.png"

type importingAdapter struct {
	reactingAdapter
	importMutex sync.Mutex
	requests    []connectors.InputAttachmentImportRequest
}

func (adapter *importingAdapter) ImportInputAttachments(_ context.Context, request connectors.InputAttachmentImportRequest) (connectors.InputAttachmentImportResult, error) {
	adapter.importMutex.Lock()
	defer adapter.importMutex.Unlock()
	adapter.requests = append(adapter.requests, request)
	return connectors.InputAttachmentImportResult{InputParts: []agentcontract.AgentPart{{
		Type:   agentcontract.AgentPartTypeImage,
		Image:  &agentcontract.AgentImagePart{Path: inboxPicturePath, MimeType: "image/png", DataBase64: "cGljdHVyZQ=="},
		Source: agentcontract.AgentPartSource{Platform: "buzz", MessageID: request.MessageID},
	}}}, nil
}

func importingConnectorRuntime() (*connectors.ConnectorRuntime, *importingAdapter) {
	adapter := &importingAdapter{}
	connectorRuntime := connectorRuntimeForTest(nil)
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, adapter
}

func TestAPictureSentWithAMessageReachesTheTurnImported(t *testing.T) {
	launcher := &recordingLauncher{reply: "SALT"}
	connectorRuntime, adapter := importingConnectorRuntime()
	connection, _ := connectedPairWithCollaborators(t, &recordingClient{}, Collaborators{
		TaskLauncher: launcher,
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		SessionTurns: connectorRuntime,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptWithPicture(t, connection, sessionID, "dm")

	if len(adapter.requests) != 1 {
		t.Fatalf("the message's attachments were offered for import %d times, expected once", len(adapter.requests))
	}
	asked := adapter.requests[0]
	if asked.MessageID != "message-picture" || !strings.HasSuffix(asked.TargetDirectoryPath, "/person-sample/inbox/buzz/conversation-1") {
		t.Fatalf("the import was asked for %q into %q, expected message-picture into person-sample's inbox for the conversation", asked.MessageID, asked.TargetDirectoryPath)
	}
	if len(asked.InputAttachments) != 1 || asked.InputAttachments[0].URL != pictureAddress {
		t.Fatalf("the import was handed %+v, expected the attachment the message carried", asked.InputAttachments)
	}
	launched := theOnlyLaunch(t, launcher)
	if launched.AttachmentMaterialResolver == nil {
		t.Fatal("the turn has no attachment material resolver, so reading any attachment fails")
	}
	if len(launched.InputParts) != 1 || launched.InputParts[0].Image == nil || launched.InputParts[0].Image.Path != "~/inbox/buzz/conversation-1/0f1e2d3c.png" {
		t.Fatalf("the turn was given %+v, expected the imported picture at its inbox path", launched.InputParts)
	}
}

func TestAMessageTheAgentIgnoresImportsNothing(t *testing.T) {
	launcher := &recordingLauncher{}
	connectorRuntime, adapter := importingConnectorRuntime()
	connection, _ := connectedPairWithCollaborators(t, &recordingClient{}, Collaborators{
		TaskLauncher:  launcher,
		Directory:     staticDirectory{},
		TurnRouter:    scriptedRouter{},
		IntakeDecider: addressedToSomebodyElse{},
		SessionTurns:  connectorRuntime,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptWithPicture(t, connection, sessionID, "channel")

	if len(adapter.requests) != 0 {
		t.Fatalf("a message the agent does not answer was imported %d times", len(adapter.requests))
	}
}

type addressedToSomebodyElse struct{}

func (addressedToSomebodyElse) Decide(_ context.Context, request agentcontract.IntakeDecisionRequest, _ *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	decisions := agentcontract.IntakeDecisions{}
	for _, message := range request.Messages {
		decisions.Messages = append(decisions.Messages, agentcontract.IntakeMessageDecision{
			MessageID:  message.MessageID,
			Addressing: agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetHuman},
		})
	}
	return decisions, nil
}

func promptWithPicture(t *testing.T, connection *acp.ClientSideConnection, sessionID acp.SessionId, conversationType string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, errorValue := connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("이 그림에 쓰인 단어가 뭐야? ![image](" + pictureAddress + ")")},
		Meta: map[string]any{MessageMetaKey: map[string]any{
			"messageID": "message-picture",
			"context": map[string]any{
				"conversationType": conversationType,
				"inputAttachments": []any{map[string]any{
					"platform":    "buzz",
					"url":         pictureAddress,
					"messageID":   "message-picture",
					"filename":    "image",
					"contentType": "image/png",
				}},
			},
		}},
	})
	if errorValue != nil {
		t.Fatalf("prompt: %v", errorValue)
	}
}
