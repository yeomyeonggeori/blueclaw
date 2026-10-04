package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	flawedSection   = `<section data-layout="split"><p>BAD crowded 99%</p></section>`
	repairedSection = `<section data-layout="split"><p>GOOD crowded 99%</p></section>`
	coverSection    = `<section data-layout="cover"><h1>Cover</h1></section>`
)

type deckModels struct {
	claimJudge
	mutex            sync.Mutex
	reviewedImages   []string
	fixerFailure     error
	repairedSections int
}

func (models *deckModels) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if _, isVisualQuestion := request.Questions["visual_defect"]; !isVisualQuestion {
		return models.claimJudge.Decide(ctx, request)
	}
	image := string(request.Images[0].Data)
	models.mutex.Lock()
	models.reviewedImages = append(models.reviewedImages, image)
	models.mutex.Unlock()
	probabilities := map[string]float64{"none": 0.95, "crowded": 0.05}
	if strings.Contains(image, "BAD") {
		probabilities = map[string]float64{"none": 0.6, "crowded": 0.4}
	}
	answer := model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Probabilities: probabilities}
	return model.DecisionResponse{Answers: map[string]model.DecisionAnswer{"visual_defect": answer}, Usage: model.Usage{CostUSD: 0.005}}, nil
}

func (models *deckModels) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("unused")
}

func (models *deckModels) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if models.fixerFailure != nil {
		return model.StructuredResponse{}, models.fixerFailure
	}
	var payload struct {
		Section string `json:"section"`
	}
	if errorValue := json.Unmarshal([]byte(request.Messages[1].Parts[1].Text), &payload); errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	models.mutex.Lock()
	models.repairedSections++
	models.mutex.Unlock()
	rewrite := strings.ReplaceAll(payload.Section, "BAD", "GOOD")
	content, errorValue := json.Marshal(map[string]string{"section": rewrite, "change": "spread the text over two columns"})
	return model.StructuredResponse{Content: string(content), Usage: model.Usage{CostUSD: 0.007}}, errorValue
}

type deckFixture struct {
	officeContextFixture
	directoryPath string
	documentPath  string
	recordPath    string
}

func (fixture deckFixture) path(name string) string {
	return filepath.Join(fixture.directoryPath, name)
}

func (fixture deckFixture) manifestJSON(sections []string, renders []string) string {
	slides := []map[string]any{}
	for index, section := range sections {
		slides = append(slides, map[string]any{"number": index + 1, "image": fixture.path(renders[index]), "state": map[string]any{"number": index + 1}, "section": section, "measured": []any{}})
	}
	return MarshalBody(map[string]any{
		"question":  map[string]any{"instructions": "Which defect?", "options": map[string]string{"none": "clean", "crowded": "packed densely"}, "cleanOption": "none"},
		"threshold": 0.3,
		"rounds":    2,
		"fixer":     map[string]string{"instructions": "Repair the slide.", "kitGuide": "layouts: split"},
		"source":    fixture.path("slides.html"),
		"slides":    slides,
	})
}

func (fixture deckFixture) snapshotJSON(claimTexts []string, blanks []map[string]string) string {
	claims := []map[string]string{}
	for index, text := range claimTexts {
		claims = append(claims, map[string]string{"path": "slides[" + string(rune('0'+index)) + "].title", "at": "slide title", "text": text})
	}
	document := map[string]any{
		"command":      "office create",
		"arguments":    []string{fixture.documentPath, fixture.path("slides.html")},
		"deck":         fixture.path("slides.html"),
		"claims":       claims,
		"visualReview": fixture.path("manifest.json"),
	}
	if blanks != nil {
		document["blanks"] = blanks
	}
	return MarshalBody(document)
}

func newDeckFixture(t *testing.T) deckFixture {
	t.Helper()
	base := newOfficeContextFixture(t)
	directoryPath := filepath.Join(base.homePath(), "artifacts", "deck")
	fixture := deckFixture{officeContextFixture: base, directoryPath: directoryPath, documentPath: filepath.Join(base.homePath(), "documents", "deck.pptx")}
	writeTestFile(t, fixture.documentPath, "deck-bytes")
	writeTestFile(t, fixture.path("slides.html"), "<html><body>\n"+coverSection+"\n"+flawedSection+"\n</body></html>")
	writeTestFile(t, fixture.path("render-1.png"), "COVER")
	writeTestFile(t, fixture.path("render-2.png"), "BAD")
	writeTestFile(t, fixture.path("manifest.json"), fixture.manifestJSON([]string{coverSection, flawedSection}, []string{"render-1.png", "render-2.png"}))
	return fixture
}

func (fixture *deckFixture) installDeckOffice(t *testing.T, rebuiltBlanks []map[string]string, rebuiltClaims []string, exitCode string) {
	t.Helper()
	fixture.recordPath = filepath.Join(fixture.workspacePath, "office-calls.txt")
	writeTestFile(t, fixture.path("rebuilt-manifest.json"), fixture.manifestJSON([]string{coverSection, repairedSection}, []string{"render-1.png", "render-3.png"}))
	writeTestFile(t, fixture.path("rebuilt-snapshot.json"), fixture.snapshotJSON(rebuiltClaims, rebuiltBlanks))
	writeTestFile(t, fixture.path("render-3.png"), "GOOD")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + shellSingleQuoted(fixture.recordPath) + "\n" +
		"cp " + shellSingleQuoted(fixture.path("rebuilt-manifest.json")) + " " + shellSingleQuoted(fixture.path("manifest.json")) + "\n" +
		"cp " + shellSingleQuoted(fixture.path("rebuilt-snapshot.json")) + " \"$2\"" + shellSingleQuoted(officeContract.SourceSuffix) + "\n" +
		"exit " + exitCode + "\n"
	entryPath := filepath.Join(BundledSkillRootPath(fixture.workspacePath), "office", "scripts", "office")
	writeTestFile(t, entryPath, script)
	if errorValue := os.Chmod(entryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func (fixture deckFixture) deliver(t *testing.T, models *deckModels) (map[string]json.RawMessage, string) {
	t.Helper()
	fixture.builder.UseVisualReviewModels(models, models)
	fixture.request.Prompt = "덱 만들어 줘"
	result := fixture.invoke(t, "file_deliver", map[string]string{"path": "documents/deck.pptx"})
	if result.Failed() || len(result.Attachments) != 1 {
		t.Fatalf("expected the deck delivered, got %s", result.ContentText())
	}
	data := map[string]json.RawMessage{}
	if errorValue := json.Unmarshal(result.Output.Data, &data); errorValue != nil {
		t.Fatal(errorValue)
	}
	return data, result.ContentText()
}

func (fixture deckFixture) readFile(t *testing.T, path string) string {
	t.Helper()
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(content)
}

func TestAFlaggedSlideIsRewrittenInTheSourceAndTheDeckRebuiltAsThePerson(t *testing.T) {
	fixture := newDeckFixture(t)
	fixture.writeDeckSnapshot(t, nil)
	fixture.installDeckOffice(t, nil, []string{"Cover", "GOOD crowded 99%"}, "0")
	models := &deckModels{}

	data, _ := fixture.deliver(t, models)

	source := fixture.readFile(t, fixture.path("slides.html"))
	if !strings.Contains(source, repairedSection) || strings.Contains(source, "BAD") || !strings.Contains(source, coverSection) {
		t.Fatalf("expected only the second section replaced, got %s", source)
	}
	expectedCall := []string{"create", fixture.documentPath, fixture.path("slides.html")}
	if calls := officeCalls(t, fixture.recordPath); strings.Join(calls, " ") != strings.Join(expectedCall, " ") {
		t.Fatalf("expected the deck rebuilt with its recorded command %v, got %v", expectedCall, calls)
	}
	for _, expected := range []string{`"outcome":"fixed"`, `"number":2`, "spread the text over two columns", `"roundsUsed":1`} {
		if !strings.Contains(string(data["visualReview"]), expected) {
			t.Fatalf("expected %s in the result, got %s", expected, data["visualReview"])
		}
	}
}

func (fixture deckFixture) writeDeckSnapshot(t *testing.T, blanks []map[string]string) {
	t.Helper()
	writeTestFile(t, fixture.documentPath+officeContract.SourceSuffix, fixture.snapshotJSON([]string{"Cover", "BAD crowded 99%"}, blanks))
}

func TestTheBlanksOfTheSnapshotSurviveTheRebuild(t *testing.T) {
	fixture := newDeckFixture(t)
	blanks := []map[string]string{{"field": "slides[1].units[2]", "label": "시장 점유율"}}
	fixture.writeDeckSnapshot(t, blanks)
	fixture.installDeckOffice(t, []map[string]string{}, []string{"Cover", "GOOD crowded 99%"}, "0")

	fixture.deliver(t, &deckModels{})

	if snapshot := fixture.readFile(t, fixture.documentPath+officeContract.SourceSuffix); !strings.Contains(snapshot, `"blanks":[{"field":"slides[1].units[2]","label":"시장 점유율"}]`) {
		t.Fatalf("expected the blank written back into the rebuilt snapshot, got %s", snapshot)
	}
}

func TestADeckWithoutBlanksIsLeftWithoutThem(t *testing.T) {
	fixture := newDeckFixture(t)
	fixture.writeDeckSnapshot(t, nil)
	fixture.installDeckOffice(t, nil, []string{"Cover", "GOOD crowded 99%"}, "0")

	fixture.deliver(t, &deckModels{})

	if snapshot := fixture.readFile(t, fixture.documentPath+officeContract.SourceSuffix); strings.Contains(snapshot, "blanks") {
		t.Fatalf("expected no blanks invented, got %s", snapshot)
	}
}

func TestAFixedSlidesNewSentenceIsJudgedAgainstTheSourcesAndBlanked(t *testing.T) {
	fixture := newDeckFixture(t)
	fixture.writeDeckSnapshot(t, []map[string]string{{"field": "slides[0].title", "label": "표지"}})
	fixture.installDeckOffice(t, []map[string]string{}, []string{"Cover", "GOOD crowded 99%"}, "0")
	models := &deckModels{claimJudge: claimJudge{unsupportedText: "GOOD crowded 99%"}}
	fixture.builder.UseClaimDecisionModel(models)

	data, content := fixture.deliver(t, models)

	expectedBlankCall := []string{"create", fixture.documentPath, fixture.path("slides.html"), "--blank", "slides[1].title"}
	calls := officeCalls(t, fixture.recordPath)
	if len(calls) != 8 || strings.Join(calls[3:], " ") != strings.Join(expectedBlankCall, " ") {
		t.Fatalf("expected the rebuild and then the blank of the new sentence, got %v", calls)
	}
	if !strings.Contains(string(data["visualReview"]), `"recheckedClaims":1`) || !strings.Contains(string(data["claimChecks"]), `"outcome":"blanked"`) {
		t.Fatalf("expected the recheck recorded, got %s and %s", data["visualReview"], data["claimChecks"])
	}
	if !strings.Contains(content, "slide title (it said \"GOOD crowded 99%\"") {
		t.Fatalf("expected the delivery to name the blank, got %s", content)
	}
	if snapshot := fixture.readFile(t, fixture.documentPath+officeContract.SourceSuffix); !strings.Contains(snapshot, `"label":"표지"`) {
		t.Fatalf("expected the earlier blank kept, got %s", snapshot)
	}
}

func TestACleanDeckIsDeliveredWithoutAFixOrARebuild(t *testing.T) {
	fixture := newDeckFixture(t)
	writeTestFile(t, fixture.path("render-2.png"), "FINE")
	fixture.writeDeckSnapshot(t, nil)
	fixture.installDeckOffice(t, nil, nil, "0")
	models := &deckModels{}

	data, _ := fixture.deliver(t, models)

	if calls := officeCalls(t, fixture.recordPath); calls != nil || models.repairedSections != 0 {
		t.Fatalf("expected no fix and no rebuild, got %v and %d fixes", calls, models.repairedSections)
	}
	if len(models.reviewedImages) != 2 || !strings.Contains(string(data["visualReview"]), `"outcome":"clean"`) {
		t.Fatalf("expected both slides reviewed and the deck clean, got %d images and %s", len(models.reviewedImages), data["visualReview"])
	}
}

func TestAFailedRebuildDeliversTheDeckAndRestoresItsSource(t *testing.T) {
	fixture := newDeckFixture(t)
	fixture.writeDeckSnapshot(t, nil)
	fixture.installDeckOffice(t, nil, nil, "1")

	data, _ := fixture.deliver(t, &deckModels{})

	if source := fixture.readFile(t, fixture.path("slides.html")); !strings.Contains(source, flawedSection) {
		t.Fatalf("expected the source put back when the rebuild failed, got %s", source)
	}
	for _, expected := range []string{`"outcome":"review_failed"`, "the deck rebuild failed"} {
		if !strings.Contains(string(data["visualReview"]), expected) {
			t.Fatalf("expected %s recorded, got %s", expected, data["visualReview"])
		}
	}
}

func TestASlideTheFixerCouldNotRepairIsNamedForTheReply(t *testing.T) {
	fixture := newDeckFixture(t)
	fixture.writeDeckSnapshot(t, nil)
	fixture.installDeckOffice(t, nil, nil, "0")

	data, content := fixture.deliver(t, &deckModels{fixerFailure: errors.New("language model unavailable")})

	if !strings.Contains(string(data["visualReview"]), `"outcome":"leftovers"`) || !strings.Contains(content, "slide 2 (crowded)") {
		t.Fatalf("expected the leftover slide named, got %s and %s", data["visualReview"], content)
	}
}

func TestAFileWithoutAVisualReviewIsNotLookedAt(t *testing.T) {
	fixture := newDeckFixture(t)
	writeTestFile(t, fixture.documentPath+officeContract.SourceSuffix, `{"command":"office create","arguments":["a","b"]}`)
	models := &deckModels{}

	data, _ := fixture.deliver(t, models)

	if data["visualReview"] != nil || len(models.reviewedImages) != 0 {
		t.Fatalf("expected a file with no review manifest left alone, got %s", data["visualReview"])
	}
}

func TestSectionsAreReplacedBySlideNumberInDocumentOrder(t *testing.T) {
	source := "<body><section>one</section>\n<SECTION class=\"x\">two</SECTION><section>three</section></body>"

	replaced, errorValue := withReplacedSections(source, map[int]string{2: "<section>TWO</section>", 3: "<section>THREE</section>"})

	if errorValue != nil || replaced != "<body><section>one</section>\n<section>TWO</section><section>THREE</section></body>" {
		t.Fatalf("expected the second and third replaced, got %q (%v)", replaced, errorValue)
	}
	if _, errorValue := withReplacedSections(source, map[int]string{4: "<section>four</section>"}); errorValue == nil {
		t.Fatal("expected a slide past the last section refused")
	}
}
