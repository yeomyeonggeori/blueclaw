package capability

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ScenarioCapabilityCatalogVariable's twin: the catalog a product offers is the
// only place the fields it actually writes can be read. A field this struct does
// not name is dropped without a word, and the runtime behaves as though the
// product never said it.
const offeredCatalogVariable = "BLUECLAW_SCENARIO_CAPABILITY_CATALOG"

func offeredCatalogDocument(t *testing.T) []byte {
	t.Helper()
	catalogPath := strings.TrimSpace(os.Getenv(offeredCatalogVariable))
	if catalogPath == "" {
		t.Skip(offeredCatalogVariable + " names no capability tool catalog")
	}
	document, errorValue := os.ReadFile(catalogPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return document
}

func TestTheDescriptorNamesEveryFieldTheCatalogWrites(t *testing.T) {
	var catalog struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if errorValue := json.Unmarshal(offeredCatalogDocument(t), &catalog); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(catalog.Tools) == 0 {
		t.Fatal("the offered catalog holds no tool")
	}

	for _, document := range catalog.Tools {
		decoder := json.NewDecoder(bytes.NewReader(document))
		decoder.DisallowUnknownFields()
		var descriptor ToolDescriptor
		if errorValue := decoder.Decode(&descriptor); errorValue != nil {
			t.Errorf("a descriptor carries something this struct drops: %v", errorValue)
		}
	}
}

func TestADecodedDescriptorKeepsWhatItWasGiven(t *testing.T) {
	document := []byte(`{
		"name": "document_share",
		"canonicalName": "document_share",
		"namespace": "document",
		"answeredBy": "company",
		"modelName": "document_share",
		"modelVisibility": "visible",
		"modelVisible": true,
		"description": "Share a document.",
		"version": "1",
		"privacyClass": "workspace_document",
		"estimatedLatency": "high",
		"requiresUserPresence": true,
		"approvalScope": "browser",
		"worksOffline": true,
		"inputSchema": {"type":"object"},
		"inputIntentSchema": {"type":"object"},
		"outputSchema": {"type":"object"},
		"inputSchemaStrict": true,
		"outputSchemaStrict": true,
		"resultContract": {
			"schema": {"type":"object"},
			"effects": [{"objectType":"document","effect":"published","resultField":"publishedURL","effectIdentity":"url","when":{"resultField":"mode","equals":"\"publish\""}}],
			"evidenceCondition": {"resultField":"mode","equals":"\"publish\""}
		},
		"policyResource": "tool:document_share",
		"sideEffectClass": "external_publish",
		"sideEffect": "external_publish",
		"requiresApproval": true,
		"completionEvidence": {"mode":"success","action":"share_document","targetKind":"document"},
		"availability": {"state":"ok","reason":"ready"},
		"idempotency": {"supported":true,"required":true,"scope":"operation"}
	}`)

	var descriptor ToolDescriptor
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if errorValue := decoder.Decode(&descriptor); errorValue != nil {
		t.Fatal(errorValue)
	}

	for fieldName, isKept := range map[string]bool{
		"answeredBy":           descriptor.AnsweredBy == "company",
		"modelVisible":         descriptor.ModelVisible,
		"version":              descriptor.Version == "1",
		"estimatedLatency":     descriptor.EstimatedLatency == "high",
		"inputSchemaStrict":    descriptor.InputSchemaStrict,
		"outputSchemaStrict":   descriptor.OutputSchemaStrict,
		"sideEffect":           descriptor.SideEffect == "external_publish",
		"approvalScope":        descriptor.ApprovalScope == "browser",
		"availability.reason":  descriptor.Availability.Reason == "ready",
		"idempotency.required": descriptor.Idempotency.Required,
	} {
		if !isKept {
			t.Errorf("%s was given and did not survive the decode", fieldName)
		}
	}

	if descriptor.ResultContract == nil || len(descriptor.ResultContract.Effects) != 1 {
		t.Fatalf("the result contract lost its effect: %#v", descriptor.ResultContract)
	}
	if descriptor.ResultContract.Effects[0].When == nil {
		t.Error("the effect's when condition was dropped")
	}
	if descriptor.ResultContract.EvidenceCondition == nil {
		t.Error("the evidence condition was dropped")
	}
	if descriptor.CompletionEvidence == nil || descriptor.CompletionEvidence.Action != "share_document" {
		t.Errorf("the completion evidence was dropped: %#v", descriptor.CompletionEvidence)
	}
}
