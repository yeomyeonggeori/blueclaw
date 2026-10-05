//go:build llmeval

package officeclaimcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/evaltest"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

const (
	corpusVariable       = "BLUECOLLAR_CLAIM_CORPUS"
	profileVariable      = "BLUECOLLAR_CLAIM_PROFILE"
	writerVariable       = "BLUECOLLAR_CLAIM_WRITER_MODEL"
	writerEffortVariable = "BLUECOLLAR_CLAIM_WRITER_EFFORT"
	recomputeVariable    = "BLUECOLLAR_CLAIM_RECOMPUTE"
	writerEndpoint       = "https://openrouter.ai/api/v1"
)

type corpusDocument struct {
	Document     string          `json:"document"`
	Path         string          `json:"path"`
	Request      string          `json:"request"`
	Attachments  []Attachment    `json:"attachments"`
	RuntimeFacts json.RawMessage `json:"runtimeFacts"`
	Claims       []corpusClaim   `json:"claims"`
	Form         string          `json:"form"`
	Language     string          `json:"language"`
}

type corpusClaim struct {
	Claim
	Label     string `json:"label"`
	LabelName string `json:"labelName"`
	Expected  string `json:"expected"`
	Subkind   string `json:"subkind"`
}

type judgedDocument struct {
	Document     string                        `json:"document"`
	Path         string                        `json:"path"`
	Probability  map[string]map[string]float64 `json:"probabilities"`
	Kind         map[string]string             `json:"kind"`
	Defect       map[string]string             `json:"defect"`
	PromptTotal  int64                         `json:"promptTokens"`
	CostUSD      float64                       `json:"costUSD"`
	ExtraTokens  int64                         `json:"recomputePromptTokens"`
	ExtraCost    float64                       `json:"recomputeCostUSD"`
	ExtraSeconds float64                       `json:"recomputeSeconds"`
	corpus       corpusDocument
}

func TestLiveClaimKindsAgainstGradedDocuments(t *testing.T) {
	corpusPath := evaltest.RequireInput(t, corpusVariable, "point it at the claim-corpus.json exported from the graded office outputs")
	endpoint, errorValue := decisions.EndpointFromEnvironment()
	evaltest.RequireConfigured(t, "the decision model", errorValue)
	documents := readCorpus(t, corpusPath)
	judged := make([]judgedDocument, len(documents))
	var group sync.WaitGroup
	limiter := make(chan struct{}, 8)
	for index, document := range documents {
		group.Add(1)
		go func() {
			defer group.Done()
			limiter <- struct{}{}
			defer func() { <-limiter }()
			judged[index] = judgeCorpusDocument(t, endpoint, document)
		}()
	}
	group.Wait()
	t.Log(costLine(judged))
	if output := os.Getenv("BLUECOLLAR_CLAIM_RESULTS"); output != "" {
		writeResults(t, output, judged)
	}
}

func readCorpus(t *testing.T, corpusPath string) []corpusDocument {
	t.Helper()
	content, errorValue := os.ReadFile(corpusPath)
	if errorValue != nil {
		t.Fatalf("read %s: %v", corpusPath, errorValue)
	}
	var corpus struct {
		Documents []corpusDocument `json:"documents"`
	}
	if errorValue := json.Unmarshal(content, &corpus); errorValue != nil || len(corpus.Documents) == 0 {
		t.Fatalf("%s holds no documents: %v", corpusPath, errorValue)
	}
	return corpus.Documents
}

func profileFromEnvironment(t *testing.T) Profile {
	t.Helper()
	switch name := os.Getenv(profileVariable); name {
	case "", CompactProfile.Name:
		return CompactProfile
	case TodayProfile.Name:
		return TodayProfile
	default:
		t.Fatalf("%s=%q names no profile; use today or compact", profileVariable, name)
		return Profile{}
	}
}

func judgeCorpusDocument(t *testing.T, endpoint decisions.Endpoint, document corpusDocument) judgedDocument {
	result := judgedDocument{Document: document.Document, Path: document.Path, Probability: map[string]map[string]float64{}, Kind: map[string]string{}, Defect: map[string]string{}, corpus: document}
	claims := []Claim{}
	for _, claim := range document.Claims {
		claims = append(claims, claim.Claim)
	}
	if len(claims) == 0 {
		return result
	}
	sources := Sources{Request: []string{document.Request}, Attachments: document.Attachments, RuntimeFacts: document.RuntimeFacts}
	judgment, errorValue := JudgeWith(context.Background(), profileFromEnvironment(t), endpoint.DecisionModel(), sources, claims)
	if errorValue != nil {
		t.Errorf("%s: %v", document.Document, errorValue)
		return result
	}
	for _, verdict := range judgment.Verdicts {
		key := claimKey(verdict.Claim)
		result.Probability[key] = verdict.Probabilities
		result.Kind[key] = verdict.Kind
		result.Defect[key] = verdict.Defect
	}
	if os.Getenv(recomputeVariable) == "1" {
		recomputeStart := time.Now()
		rechecked, errorValue := Recompute(context.Background(), profileFromEnvironment(t), writerFromEnvironment(t, endpoint), sources, judgment)
		if errorValue != nil {
			t.Errorf("%s: %v", document.Document, errorValue)
		} else {
			result.ExtraSeconds = time.Since(recomputeStart).Seconds()
			markRecomputed(&result, judgment, rechecked)
			result.ExtraTokens = rechecked.Usage.PromptTokens - judgment.Usage.PromptTokens
			result.ExtraCost = rechecked.Usage.CostUSD - judgment.Usage.CostUSD
		}
	}
	result.PromptTotal = judgment.Usage.PromptTokens
	result.CostUSD = judgment.Usage.CostUSD
	return result
}

func claimKey(claim Claim) string {
	return claim.Path + "\x00" + claim.Text
}

func costLine(judged []judgedDocument) string {
	prompts := []int64{}
	total := 0.0
	for _, document := range judged {
		if document.PromptTotal > 0 {
			prompts = append(prompts, document.PromptTotal)
		}
		total += document.CostUSD
	}
	sort.Slice(prompts, func(left, right int) bool { return prompts[left] < prompts[right] })
	if len(prompts) == 0 {
		return "no decision calls"
	}
	return fmt.Sprintf("%d documents asked, median prompt %d tokens, total cost USD %.5f", len(prompts), prompts[len(prompts)/2], total)
}

func writeResults(t *testing.T, output string, judged []judgedDocument) {
	t.Helper()
	content, errorValue := json.MarshalIndent(judged, "", " ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(output, content, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func writerFromEnvironment(t *testing.T, endpoint decisions.Endpoint) model.LanguageModelProvider {
	t.Helper()
	name := evaltest.RequireInput(t, writerVariable, "name the language model that recomputes and rewrites, as OpenRouter spells it")
	provider, errorValue := openaicompatible.Endpoint{URL: writerEndpoint, ModelName: name, APIKey: endpoint.APIKey, ProviderSort: "throughput", ReasoningEffort: os.Getenv(writerEffortVariable)}.Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return provider
}

func markRecomputed(result *judgedDocument, before Judgment, after Judgment) {
	for index, verdict := range after.Verdicts {
		if verdict.Defect == KindError && before.Verdicts[index].Defect != KindError {
			result.Probability[claimKey(verdict.Claim)] = map[string]float64{KindError: 1}
			result.Defect[claimKey(verdict.Claim)] = KindError
		}
	}
}

type treatedUnit struct {
	Document string `json:"document"`
	Original string `json:"original"`
	Expected string `json:"expected"`
	Subkind  string `json:"subkind"`
	Defect   string `json:"defect"`
	Outcome  string `json:"outcome"`
	Text     string `json:"text,omitempty"`
}

type treatedDocument struct {
	Document string         `json:"document"`
	Form     string         `json:"form"`
	Language string         `json:"language"`
	Request  string         `json:"request"`
	Original []string       `json:"original"`
	After    []string       `json:"after"`
	Changes  []treatedUnit  `json:"changes"`
	Defects  map[string]int `json:"defects"`
	Seconds  float64        `json:"treatSeconds"`
	CostUSD  float64        `json:"treatCostUSD"`
	Prompt   int64          `json:"treatPromptTokens"`
}

func TestLiveRewriteTreatmentOfFlaggedUnits(t *testing.T) {
	corpusPath := evaltest.RequireInput(t, corpusVariable, "point it at the defect corpus")
	endpoint, errorValue := decisions.EndpointFromEnvironment()
	evaltest.RequireConfigured(t, "the decision model", errorValue)
	profile := profileFromEnvironment(t)
	writer := writerFromEnvironment(t, endpoint)
	documents := readCorpus(t, corpusPath)
	treated := make([]treatedDocument, len(documents))
	var group sync.WaitGroup
	limiter := make(chan struct{}, 8)
	for index, document := range documents {
		group.Add(1)
		go func() {
			defer group.Done()
			limiter <- struct{}{}
			defer func() { <-limiter }()
			treated[index] = treatDocument(t, profile, endpoint, writer, document)
		}()
	}
	group.Wait()
	counts := map[string]int{}
	for _, document := range treated {
		for _, unit := range document.Changes {
			counts[unit.Defect+"/"+unit.Expected+"/"+unit.Outcome]++
		}
	}
	t.Logf("treatment outcomes (defect/expected/outcome): %v", counts)
	if output := os.Getenv("BLUECOLLAR_CLAIM_RESULTS"); output != "" {
		content, _ := json.MarshalIndent(treated, "", " ")
		if errorValue := os.WriteFile(output, content, 0o600); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
}

func treatDocument(t *testing.T, profile Profile, endpoint decisions.Endpoint, writer model.LanguageModelProvider, document corpusDocument) treatedDocument {
	result := treatedDocument{Document: document.Document, Form: document.Form, Language: document.Language, Request: document.Request}
	claims := []Claim{}
	expected := map[string]corpusClaim{}
	for _, claim := range document.Claims {
		claim.IsFree = os.Getenv("BLUECOLLAR_CLAIM_ALL_FREE") == "1"
		claims = append(claims, claim.Claim)
		expected[claim.Path] = claim
		result.Original = append(result.Original, claim.Text)
	}
	sources := Sources{Request: []string{document.Request}, Attachments: document.Attachments, RuntimeFacts: document.RuntimeFacts}
	judgment, errorValue := JudgeWith(context.Background(), profile, endpoint.DecisionModel(), sources, claims)
	if errorValue != nil {
		t.Errorf("%s: %v", document.Document, errorValue)
		return result
	}
	result.Defects = map[string]int{}
	for _, verdict := range judgment.Flagged() {
		result.Defects[verdict.Defect]++
	}
	treatStart := time.Now()
	outcome, errorValue := Treat(context.Background(), profile, endpoint.DecisionModel(), writer, sources, judgment)
	if errorValue != nil {
		t.Errorf("%s: %v", document.Document, errorValue)
		return result
	}
	result.Seconds, result.CostUSD, result.Prompt = time.Since(treatStart).Seconds(), outcome.Usage.CostUSD, outcome.Usage.PromptTokens
	replaced := map[string]string{}
	for _, claim := range outcome.Replaced {
		replaced[claim.Path] = claim.Text
		result.Changes = append(result.Changes, unitOf(expected[claim.Path], judgment, "replaced", claim.Text))
	}
	removed := map[string]bool{}
	for _, verdict := range outcome.Removed {
		removed[verdict.Path] = true
		result.Changes = append(result.Changes, unitOf(expected[verdict.Path], judgment, "removed", ""))
	}
	for _, verdict := range outcome.Kept {
		result.Changes = append(result.Changes, unitOf(expected[verdict.Path], judgment, "kept", ""))
	}
	for _, claim := range document.Claims {
		if removed[claim.Path] {
			continue
		}
		if text, isReplaced := replaced[claim.Path]; isReplaced {
			result.After = append(result.After, text)
			continue
		}
		result.After = append(result.After, claim.Text)
	}
	return result
}

func unitOf(claim corpusClaim, judgment Judgment, outcome string, text string) treatedUnit {
	defect := ""
	for _, verdict := range judgment.Verdicts {
		if verdict.Path == claim.Path && verdict.Text == claim.Text {
			defect = verdict.Defect
		}
	}
	expected := claim.Expected
	if claim.Label != "seed" {
		expected = "clean:" + claim.Expected
	}
	return treatedUnit{Original: claim.Text, Expected: expected, Subkind: claim.Subkind, Defect: defect, Outcome: outcome, Text: text}
}
