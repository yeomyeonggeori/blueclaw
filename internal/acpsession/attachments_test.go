package acpsession

import (
	"context"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const pictureAddress = "http://127.0.0.1:3000/media/0f1e2d3c.png"
const importedPicturePath = "~/inbox/buzz/dm/0f1e2d3c.png"

type recordingAttachmentImporter struct {
	mutex    sync.Mutex
	asked    []connectors.PlatformInboundEvent
	askedFor []string
}

func (importer *recordingAttachmentImporter) ImportMessageAttachments(_ context.Context, event connectors.PlatformInboundEvent, personID string) (connectors.PlatformInboundEvent, agentruntime.AttachmentMaterialResolver) {
	importer.mutex.Lock()
	importer.asked = append(importer.asked, event)
	importer.askedFor = append(importer.askedFor, personID)
	importer.mutex.Unlock()
	imported := event
	imported.Context.InputAttachments = []connectors.InputAttachment{{
		Platform:    "buzz",
		URL:         pictureAddress,
		MessageID:   event.MessageID,
		Filename:    "0f1e2d3c.png",
		ContentType: "image/png",
		Path:        importedPicturePath,
		IsAvailable: true,
	}}
	imported.InputParts = []agentcontract.AgentPart{{
		Type:   agentcontract.AgentPartTypeImage,
		Image:  &agentcontract.AgentImagePart{Path: importedPicturePath, MimeType: "image/png", DataBase64: "cGljdHVyZQ=="},
		Source: agentcontract.AgentPartSource{Platform: "buzz", MessageID: event.MessageID},
	}}
	return imported, importer
}

func (importer *recordingAttachmentImporter) ResolveAttachmentMaterial(context.Context, string) (agentcontract.VisibleContextMaterial, error) {
	return agentcontract.VisibleContextMaterial{Path: importedPicturePath}, nil
}

func TestAPictureSentWithAMessageReachesTheTurnImported(t *testing.T) {
	launcher := &recordingLauncher{reply: "SALT"}
	importer := &recordingAttachmentImporter{}
	client := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher:       launcher,
		Directory:          staticDirectory{},
		ReplyReader:        scriptedReader{},
		AttachmentImporter: importer,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptWithPicture(t, connection, sessionID, "dm")

	if len(importer.asked) != 1 {
		t.Fatalf("the message's attachments were offered for import %d times, expected once", len(importer.asked))
	}
	asked := importer.asked[0]
	if importer.askedFor[0] != "person-sample" || asked.Platform != "buzz" || asked.MessageID != "message-picture" {
		t.Fatalf("the import was asked for %q on %q/%q, expected person-sample on buzz/message-picture", importer.askedFor[0], asked.Platform, asked.MessageID)
	}
	if len(asked.Context.InputAttachments) != 1 || asked.Context.InputAttachments[0].URL != pictureAddress {
		t.Fatalf("the import was handed %+v, expected the attachment the message carried", asked.Context.InputAttachments)
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
	launcher := &recordingLauncher{}
	importer := &recordingAttachmentImporter{}
	client := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher:       launcher,
		Directory:          staticDirectory{},
		ReplyReader:        scriptedReader{},
		SessionTurns:       connectorRuntimeDeciding(nil, addressedToSomebodyElse{}),
		AttachmentImporter: importer,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptWithPicture(t, connection, sessionID, "channel")

	if len(importer.asked) != 0 {
		t.Fatalf("a message the agent does not answer was imported %d times", len(importer.asked))
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
