//go:build appliance && llmeval && !nobundledharness

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestOfficeQuoteForAnEnglishCompanyLive(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Fatal("OPENROUTER_API_KEY is required: building with the llmeval tag is the request to run this evaluation")
	}
	request := officeExampleRequest{
		Name:        "english_company_quote",
		Text:        "Please make a quote PDF on our company letterhead for Example Client Ltd.: website build, 1 unit at 12,000 USD.",
		Deliverable: "pdf",
	}
	outputDirectory := officeCompanyLanguageOutputDirectory(t, request.Name)
	result := runOfficeExample(t, apiKey, request, outputDirectory, outputDirectory, englishExampleCompany())
	if shortfall := officeExampleShortfall(request, result); shortfall != "" {
		t.Fatalf("office example %s: %s", request.Name, shortfall)
	}
	merged := officeFormsMerged(result.TurnResults[len(result.TurnResults)-1].Events)
	if !slices.Contains(merged, "intl/quote") || slices.ContainsFunc(merged, isKoreanForm) {
		t.Fatalf("an English company's quote merged %v, expected intl/quote and no kr form; evidence in %s", merged, outputDirectory)
	}
}

func englishExampleCompany() officeExampleCompany {
	return officeExampleCompany{Language: "en", Profiles: map[string]map[string]any{"en": {
		"name":                   "Example Works Inc.",
		"brandName":              "",
		"slogan":                 "",
		"description":            "",
		"representative":         "Sam Sample",
		"representativeTitle":    "CEO",
		"representativePersonID": nil,
		"address":                "1 Example Street, Example City",
		"officeAddress":          "",
		"jurisdiction":           "",
		"bankAccount":            "Example Bank 000-000-000 Example Works Inc.",
		"legalAttributes":        []any{},
		"foundedDate":            "",
		"capital":                nil,
		"fiscalYearEnd":          "",
		"employeeCount":          3,
		"phone":                  "+1-555-0100",
		"fax":                    "",
		"email":                  "hello@example.com",
		"website":                "",
		"missingFields":          []any{},
		"updatedAt":              "",
	}}}
}

func officeFormsMerged(events []task.TaskEvent) []string {
	merged := []string{}
	for _, event := range events {
		if event.Name != agentcontract.TaskEventAgentAction {
			continue
		}
		var action struct {
			ToolInput struct {
				Command string `json:"command"`
			} `json:"toolInput"`
		}
		if json.Unmarshal([]byte(event.Body), &action) != nil {
			continue
		}
		merged = append(merged, formsAfterMerge(action.ToolInput.Command)...)
	}
	return merged
}

func formsAfterMerge(command string) []string {
	words := strings.Fields(command)
	forms := []string{}
	for index := 0; index+1 < len(words); index++ {
		form := strings.Trim(words[index+1], `"'`)
		if words[index] == "merge" && !isGuideLookup(words, index) && !strings.HasPrefix(form, "-") {
			forms = append(forms, form)
		}
	}
	return forms
}

func isGuideLookup(words []string, index int) bool {
	return index > 0 && words[index-1] == "guide"
}

func isKoreanForm(form string) bool {
	return strings.HasPrefix(form, "kr/")
}

func officeCompanyLanguageOutputDirectory(t *testing.T, name string) string {
	t.Helper()
	artifactRoot := firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_ARTIFACT_DIR"), filepath.Join("..", "..", ".artifacts", "office-company-language-live"))
	directory, errorValue := filepath.Abs(filepath.Join(artifactRoot, fmt.Sprintf("%s-%d", name, time.Now().UnixNano())))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.MkdirAll(directory, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	return directory
}
