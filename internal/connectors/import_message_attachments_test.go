//go:build !nobundledharness

package connectors

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const relayPictureAddress = "http://127.0.0.1:3000/media/0f1e2d3c.png"

func TestImportMessageAttachmentsBringsAMessagesPictureIntoTheRequestersInbox(t *testing.T) {
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

	imported, resolver := connectorRuntime.ImportMessageAttachments(context.Background(), event, "person-1")

	if len(adapter.inputAttachmentImportRequests) != 1 {
		t.Fatalf("the adapter was asked to import %d times, expected once", len(adapter.inputAttachmentImportRequests))
	}
	if target := adapter.inputAttachmentImportRequests[0].TargetDirectoryPath; !strings.HasSuffix(target, "/inbox/test/dm") {
		t.Fatalf("the picture was imported into %q, expected the requester's dm inbox", target)
	}
	if len(imported.Context.InputAttachments) != 1 || imported.Context.InputAttachments[0].Path != "~/inbox/test/dm/0f1e2d3c.png" {
		t.Fatalf("the message now carries %+v, expected the picture at its inbox path", imported.Context.InputAttachments)
	}
	if len(imported.InputParts) != 1 || imported.InputParts[0].Image == nil {
		t.Fatalf("the message now carries parts %+v, expected the picture", imported.InputParts)
	}
	if resolver == nil {
		t.Fatal("no resolver came back, so the turn cannot read an attachment the conversation shows")
	}
	material, errorValue := resolver.ResolveAttachmentMaterial(context.Background(), relayPictureAddress)
	if errorValue != nil {
		t.Fatalf("the picture did not resolve by the address the message showed: %v", errorValue)
	}
	if material.Path != "~/inbox/test/dm/0f1e2d3c.png" {
		t.Fatalf("the picture resolved to %q, expected its inbox path", material.Path)
	}
}

func TestImportMessageAttachmentsLeavesAMessageFromAnUnknownPlatformAsItCame(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	event := testInboundEvent("message-picture")
	event.Platform = "elsewhere"
	event.Context.InputAttachments = []InputAttachment{{Platform: "elsewhere", URL: relayPictureAddress}}

	imported, resolver := connectorRuntime.ImportMessageAttachments(context.Background(), event, "person-1")

	if resolver != nil {
		t.Fatalf("a platform nothing serves was given a resolver %+v", resolver)
	}
	if len(adapter.inputAttachmentImportRequests) != 0 {
		t.Fatalf("another platform's adapter was asked to import %+v", adapter.inputAttachmentImportRequests)
	}
	if len(imported.Context.InputAttachments) != 1 || imported.Context.InputAttachments[0].URL != relayPictureAddress {
		t.Fatalf("the message changed to %+v", imported.Context.InputAttachments)
	}
}
