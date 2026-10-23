package acpsession

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const pictureAddress = "http://127.0.0.1:3000/media/0f1e2d3c.png"
const importedPicturePath = "~/inbox/buzz/dm/0f1e2d3c.png"

const importedPictureWorkspacePath = "/workspace/private/people/person-sample/inbox/buzz/dm/0f1e2d3c.png"

type importingAdapter struct {
	reactingAdapter
	importMutex sync.Mutex
	asked       []connectors.InputAttachmentImportRequest
}

func (adapter *importingAdapter) ImportInputAttachments(_ context.Context, request connectors.InputAttachmentImportRequest) (connectors.InputAttachmentImportResult, error) {
	adapter.importMutex.Lock()
	defer adapter.importMutex.Unlock()
	adapter.asked = append(adapter.asked, request)
	return connectors.InputAttachmentImportResult{
		InputParts: []agentcontract.AgentPart{{
			Type:   agentcontract.AgentPartTypeImage,
			Image:  &agentcontract.AgentImagePart{Path: importedPictureWorkspacePath, MimeType: "image/png", DataBase64: "cGljdHVyZQ=="},
			Source: agentcontract.AgentPartSource{Platform: "buzz", MessageID: request.MessageID},
		}},
		InputAttachments: []connectors.InputAttachment{{
			Platform:    "buzz",
			URL:         pictureAddress,
			MessageID:   request.MessageID,
			Filename:    "0f1e2d3c.png",
			ContentType: "image/png",
			Path:        importedPictureWorkspacePath,
			IsAvailable: true,
		}},
	}, nil
}

func (adapter *importingAdapter) importRequests() []connectors.InputAttachmentImportRequest {
	adapter.importMutex.Lock()
	defer adapter.importMutex.Unlock()
	return append([]connectors.InputAttachmentImportRequest{}, adapter.asked...)
}

func importingConnectorRuntime(gatewayDecider inboundengagement.Decider) (*connectors.ConnectorRuntime, *importingAdapter) {
	adapter := &importingAdapter{}
	connectorRuntime := connectorRuntimeDeciding(nil, gatewayDecider)
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, adapter
}

func TestAPictureSentWithAMessageReachesTheTurnImported(t *testing.T) {
	launcher := &recordingLauncher{reply: "SALT"}
	connectorRuntime, adapter := importingConnectorRuntime(addressedToTheAgent{})
	connection, _ := connectedPairWithCollaborators(t, &recordingClient{}, Collaborators{
		TaskLauncher: launcher,
		Directory:    staticDirectory{},
		ReplyReader:  scriptedReader{},
		SessionTurns: connectorRuntime,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptWithPicture(t, connection, sessionID, "dm")

	asked := adapter.importRequests()
	if len(asked) != 1 {
		t.Fatalf("the message's attachments were offered for import %d times, expected once", len(asked))
	}
	if asked[0].MessageID != "message-picture" || len(asked[0].InputAttachments) != 1 || asked[0].InputAttachments[0].URL != pictureAddress {
		t.Fatalf("the import was asked for %+v, expected the attachment message-picture carried", asked[0])
	}
	if !strings.Contains(asked[0].TargetDirectoryPath, "person-sample") {
		t.Fatalf("the picture was imported into %q, expected person-sample's inbox", asked[0].TargetDirectoryPath)
	}
	launched := theOnlyLaunch(t, launcher)
	if launched.AttachmentMaterialResolver == nil {
		t.Fatal("the turn has no attachment material resolver, so reading any attachment fails")
	}
	if len(launched.InputParts) != 1 || launched.InputParts[0].Image == nil || launched.InputParts[0].Image.Path != importedPicturePath {
		t.Fatalf("the turn was given %+v, expected the imported picture", launched.InputParts)
	}
	current := launched.VisibleContext.CurrentMaterials
	if len(current) != 1 || current[0].Path != importedPicturePath {
		t.Fatalf("the turn sees %+v as the message's attachments, expected the imported path", current)
	}
}

func TestAMessageTheAgentIgnoresImportsNothing(t *testing.T) {
	connectorRuntime, adapter := importingConnectorRuntime(addressedToSomebodyElse{})
	connection, _ := connectedPairWithCollaborators(t, &recordingClient{}, Collaborators{
		TaskLauncher: &recordingLauncher{},
		Directory:    staticDirectory{},
		ReplyReader:  scriptedReader{},
		SessionTurns: connectorRuntime,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptWithPicture(t, connection, sessionID, "channel")

	if asked := adapter.importRequests(); len(asked) != 0 {
		t.Fatalf("a message the agent does not answer was imported %d times", len(asked))
	}
}

type addressedToSomebodyElse struct{}

func (addressedToSomebodyElse) Decide(_ context.Context, facts inboundengagement.Facts, _ agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	judgments := []inboundengagement.Judgment{}
	for _, message := range facts.Messages {
		judgments = append(judgments, inboundengagement.Judgment{
			MessageID:  message.MessageID,
			Addressing: inboundengagement.AddressingDecision{Target: inboundengagement.AddressingTargetHuman},
		})
	}
	return judgments, nil
}

func (addressedToSomebodyElse) FitsBurstBudget(inboundengagement.Facts) bool {
	return true
}

type addressedToTheAgent struct{}

func (addressedToTheAgent) Decide(_ context.Context, facts inboundengagement.Facts, _ agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	judgments := []inboundengagement.Judgment{}
	for _, message := range facts.Messages {
		judgments = append(judgments, inboundengagement.Judgment{
			MessageID:  message.MessageID,
			Addressing: inboundengagement.AddressingDecision{Target: inboundengagement.AddressingTargetBot, ShouldRespond: true},
		})
	}
	return judgments, nil
}

func (addressedToTheAgent) FitsBurstBudget(inboundengagement.Facts) bool {
	return true
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
