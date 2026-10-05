package agentruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

const deckLayoutsRequest = `{"instructions":"Choose the layout of each page.","questions":{"page_01":{"instructions":"Page 1 of 2, a cover page.","options":{"cover_dark_minimal":"dark","cover_typography_hero":"type"}},"page_02":{"instructions":"Page 2 of 2, a content page.","options":{"hero_big_number":"number","three_column_cards":"three"}}}}`

func (fixture officeContextFixture) writeDeckLayoutsRequest(t *testing.T, content string) {
	t.Helper()
	result := fixture.invoke(t, "bash", map[string]any{"command": `printf '%s' '` + content + `' > "$(dirname "$OFFICE_RUNTIME_CONTEXT")/` + officeContract.DeckLayouts.RequestFile + `"`})
	if result.Failed() {
		t.Fatalf("the shell could not write the request: %s", result.ContentText())
	}
}

func TestTheHostChoosesEveryPagesLayoutInOneDecisionAndAnswersWithTheRequestsDigest(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	decisions := &designDecisionStandIn{}
	fixture.builder.UseDeckDesignModel(decisions)
	fixture.request.Prompt = "Make a strategy deck."
	if told := string(fixture.contextTheShellReads(t)["choosesDeckLayouts"]); told != "true" {
		t.Fatalf("a host with a deck design model told the office it chooses layouts: %s", told)
	}

	fixture.writeDeckLayoutsRequest(t, deckLayoutsRequest)
	answer := fixture.contextTheShellReads(t)["deckLayouts"]

	var layouts officeDeckLayouts
	if errorValue := json.Unmarshal(answer, &layouts); errorValue != nil {
		t.Fatalf("deckLayouts is %s", answer)
	}
	digest := sha256.Sum256([]byte(deckLayoutsRequest))
	if layouts.Digest != hex.EncodeToString(digest[:]) || len(layouts.Choices) != 2 || layouts.Choices["page_02"].Option != "first" || layouts.Model != "design-stand-in" {
		t.Fatalf("deckLayouts is %s", answer)
	}
	if len(decisions.asked) != 1 || len(decisions.asked[0].Questions) != 2 {
		t.Fatalf("asked %d decisions, want one holding both pages", len(decisions.asked))
	}
}

func TestLayoutsTheHostAlreadyChoseForTheSameOutlineAreNotAskedAgain(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	decisions := &designDecisionStandIn{}
	fixture.builder.UseDeckDesignModel(decisions)
	fixture.writeDeckLayoutsRequest(t, deckLayoutsRequest)
	fixture.contextTheShellReads(t)

	fixture.contextTheShellReads(t)

	if len(decisions.asked) != 1 {
		t.Fatalf("asked %d times for one outline", len(decisions.asked))
	}
}

func TestAHostWithoutADeckDesignModelNeitherChoosesLayoutsNorAnnouncesIt(t *testing.T) {
	fixture := newOfficeContextFixture(t)
	fixture.writeDeckLayoutsRequest(t, deckLayoutsRequest)
	document := fixture.contextTheShellReads(t)
	if string(document["choosesDeckLayouts"]) != "false" || string(document["deckLayouts"]) != "null" {
		t.Fatalf("chooses %s, layouts %s", document["choosesDeckLayouts"], document["deckLayouts"])
	}
}
