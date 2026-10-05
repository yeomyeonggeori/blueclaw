package acpsession

import (
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

const clientContractPath = "client_contract.json"

var shouldRewriteClientContract = flag.Bool("rewrite-client-contract", false, "rewrite "+clientContractPath+" from the Go declarations")

type clientContract struct {
	MetaKeys         map[string]string   `json:"metaKeys"`
	ExtensionMethods map[string]string   `json:"extensionMethods"`
	Fields           map[string][]string `json:"fields"`
	ToolCalls        toolCallContract    `json:"toolCalls"`
}

type toolCallContract struct {
	StartKind    string   `json:"startKind"`
	StartFields  []string `json:"startFields"`
	UpdateKind   string   `json:"updateKind"`
	UpdateFields []string `json:"updateFields"`
	Statuses     []string `json:"statuses"`
}

func clientContractDeclared() clientContract {
	return clientContract{
		MetaKeys: map[string]string{
			"session":  SessionMetaKey,
			"message":  MessageMetaKey,
			"delivery": DeliveryMetaKey,
		},
		ExtensionMethods: map[string]string{
			"approvalReply": ApprovalReplyExtensionMethod,
			"delivered":     DeliveredExtensionMethod,
			"undelivered":   UndeliveredExtensionMethod,
		},
		Fields: map[string][]string{
			"delivery":            jsonFieldNamesOf(Delivery{}),
			"delivered":           jsonFieldNamesOf(DeliveredReport{}),
			"undelivered":         jsonFieldNamesOf(UndeliveredReport{}),
			"approvalReply":       jsonFieldNamesOf(ApprovalReplyRequest{}),
			"approvalReplyAnswer": jsonFieldNamesOf(ApprovalReplyResponse{}),
		},
		ToolCalls: toolCallContract{
			StartKind:    wireKindOf(startedToolCallForContract()),
			StartFields:  wireFieldNamesOf(startedToolCallForContract()),
			UpdateKind:   wireKindOf(completedToolCallForContract()),
			UpdateFields: wireFieldNamesOf(completedToolCallForContract()),
			Statuses: []string{
				string(acp.ToolCallStatusPending),
				string(acp.ToolCallStatusInProgress),
				string(acp.ToolCallStatusCompleted),
				string(acp.ToolCallStatusFailed),
			},
		},
	}
}

func startedToolCallForContract() acp.SessionUpdate {
	return acp.StartToolCall("call-1", "title", acp.WithStartStatus(acp.ToolCallStatusPending))
}

func completedToolCallForContract() acp.SessionUpdate {
	return acp.UpdateToolCall("call-1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted))
}

func wireKindOf(update acp.SessionUpdate) string {
	document, _ := json.Marshal(update)
	wire := struct {
		SessionUpdate string `json:"sessionUpdate"`
	}{}
	json.Unmarshal(document, &wire)
	return wire.SessionUpdate
}

func wireOf(update acp.SessionUpdate) map[string]any {
	document, _ := json.Marshal(update)
	wire := map[string]any{}
	json.Unmarshal(document, &wire)
	return wire
}

func wireFieldNamesOf(update acp.SessionUpdate) []string {
	names := []string{}
	for name := range wireOf(update) {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func jsonFieldNamesOf(value any) []string {
	valueType := reflect.TypeOf(value)
	names := []string{}
	for index := range valueType.NumField() {
		name, _, _ := strings.Cut(valueType.Field(index).Tag.Get("json"), ",")
		names = append(names, name)
	}
	return names
}

func TestTheClientContractFileMatchesTheGoDeclarations(t *testing.T) {
	declared, errorValue := json.MarshalIndent(clientContractDeclared(), "", "\t")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	declared = append(declared, '\n')
	if *shouldRewriteClientContract {
		if errorValue := os.WriteFile(clientContractPath, declared, 0o644); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	written, errorValue := os.ReadFile(clientContractPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(written) != string(declared) {
		t.Fatalf("%s is what a client in another language checks itself against, and it no longer says what this package declares; run go test ./internal/acpsession -run TestTheClientContractFileMatchesTheGoDeclarations -rewrite-client-contract\nwant:\n%s", clientContractPath, declared)
	}
}
