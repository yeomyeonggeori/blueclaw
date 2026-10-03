package acpsession

import (
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"
)

const clientContractPath = "client_contract.json"

var shouldRewriteClientContract = flag.Bool("rewrite-client-contract", false, "rewrite "+clientContractPath+" from the Go declarations")

type clientContract struct {
	MetaKeys         map[string]string   `json:"metaKeys"`
	ExtensionMethods map[string]string   `json:"extensionMethods"`
	Fields           map[string][]string `json:"fields"`
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
	}
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
