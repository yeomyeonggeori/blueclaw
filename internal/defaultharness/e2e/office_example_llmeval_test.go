//go:build appliance && llmeval && !nobundledharness

package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/model/decisions"
	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
)

const (
	officeExampleModelEndpoint      = "https://openrouter.ai/api/v1"
	officeExampleDecisionsEndpoint  = "https://openrouter.ai/api/alpha/decisions"
	officeExamplePrimaryModel       = "z-ai/glm-5.3-flash"
	officeExampleDecisionModel      = "~typesafe/jev-latest"
	officeExampleVisualReviewModel  = "cloudflare/clef"
	officeExampleProviderSort       = "throughput"
	officeExampleCompanyInfoTool    = "company_info_get"
	officeExampleCompanyProfileName = "company-profile.json"
)

type officeExampleRequest struct {
	Name           string   `json:"name"`
	Text           string   `json:"text"`
	Attachments    []string `json:"attachments"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

type officeExampleModels struct {
	Primary      string `json:"primary"`
	Decision     string `json:"decision"`
	VisualReview string `json:"visualReview"`
}

type recordedDecision struct {
	Label     string                            `json:"label"`
	StartedAt time.Time                         `json:"startedAt"`
	Seconds   float64                           `json:"seconds"`
	State     any                               `json:"state"`
	Questions map[string]model.DecisionQuestion `json:"questions"`
	Images    []string                          `json:"images,omitempty"`
	Response  model.DecisionResponse            `json:"response"`
	Error     string                            `json:"error,omitempty"`
}

type decisionRecorder struct {
	label     string
	delegate  model.DecisionModel
	directory string
	mutex     sync.Mutex
	sequence  int
}

func (recorder *decisionRecorder) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	started := time.Now()
	response, errorValue := recorder.delegate.Decide(ctx, request)
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.sequence++
	entry := recordedDecision{Label: recorder.label, StartedAt: started, Seconds: time.Since(started).Seconds(), State: request.State, Questions: request.Questions, Response: response}
	if errorValue != nil {
		entry.Error = errorValue.Error()
	}
	for index, image := range request.Images {
		name := fmt.Sprintf("%s-%04d-%d.png", recorder.label, recorder.sequence, index)
		_ = os.WriteFile(filepath.Join(recorder.directory, "decision-images", name), image.Data, 0o644)
		entry.Images = append(entry.Images, name)
	}
	appendJSONLine(filepath.Join(recorder.directory, "decisions.jsonl"), entry)
	return response, errorValue
}

type recordedLanguageCall struct {
	Label     string      `json:"label"`
	StartedAt time.Time   `json:"startedAt"`
	Seconds   float64     `json:"seconds"`
	Prompt    string      `json:"prompt"`
	Content   string      `json:"content"`
	Usage     model.Usage `json:"usage"`
	Error     string      `json:"error,omitempty"`
}

type languageRecorder struct {
	label     string
	delegate  llm.LanguageModelProvider
	directory string
}

func (recorder languageRecorder) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	return recorder.delegate.GenerateResponse(ctx, prompt)
}

func (recorder languageRecorder) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	started := time.Now()
	response, errorValue := recorder.delegate.GenerateStructuredResponse(ctx, request)
	entry := recordedLanguageCall{Label: recorder.label, StartedAt: started, Seconds: time.Since(started).Seconds(), Prompt: textOfMessages(request.Messages), Content: response.Content, Usage: response.Usage}
	if errorValue != nil {
		entry.Error = errorValue.Error()
	}
	appendJSONLine(filepath.Join(recorder.directory, "fixer-calls.jsonl"), entry)
	return response, errorValue
}

var appendMutex sync.Mutex

func appendJSONLine(path string, entry any) {
	appendMutex.Lock()
	defer appendMutex.Unlock()
	line, _ := json.Marshal(entry)
	file, errorValue := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if errorValue != nil {
		return
	}
	defer file.Close()
	file.Write(append(line, '\n'))
}

func textOfMessages(messages []model.Message) string {
	parts := []string{}
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
		if message.Content != "" {
			parts = append(parts, message.Content)
		}
	}
	return strings.Join(parts, "\n---\n")
}

func officeExampleTierModel(t *testing.T, apiKey string, effort string) llm.LanguageModelProvider {
	t.Helper()
	provider, errorValue := openaicompatible.Endpoint{URL: officeExampleModelEndpoint, ModelName: officeExamplePrimaryModel, APIKey: apiKey, ProviderSort: officeExampleProviderSort, ReasoningEffort: effort}.Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return provider
}

func officeExampleAttachments(t *testing.T, requestDirectory string, names []string) []connectors.InputAttachment {
	t.Helper()
	attachments := []connectors.InputAttachment{}
	for _, name := range names {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(requestDirectory, name)
		}
		content, errorValue := os.ReadFile(path)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		contentType := mime.TypeByExtension(filepath.Ext(path))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		attachments = append(attachments, connectors.InputAttachment{Filename: filepath.Base(path), ContentType: contentType, ContentBase64: base64.StdEncoding.EncodeToString(content), IsAvailable: true})
	}
	return attachments
}

func readOfficeExampleRequest(t *testing.T, requestPath string) officeExampleRequest {
	t.Helper()
	content, errorValue := os.ReadFile(requestPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var request officeExampleRequest
	if errorValue := json.Unmarshal(content, &request); errorValue != nil {
		t.Fatal(errorValue)
	}
	return request
}

func capabilityCatalogEntry(t *testing.T, toolName string) map[string]any {
	t.Helper()
	document, errorValue := os.ReadFile(scenarioCapabilityCatalogPath())
	if errorValue != nil {
		t.Fatalf("%s must name the capability catalog: %v", ScenarioCapabilityCatalogVariable, errorValue)
	}
	var catalog struct {
		Tools []map[string]any `json:"tools"`
	}
	if errorValue := json.Unmarshal(document, &catalog); errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, entry := range catalog.Tools {
		if entry["name"] == toolName {
			return entry
		}
	}
	t.Fatalf("the capability catalog carries no %s", toolName)
	return nil
}

func readCompanyProfiles(t *testing.T, profilePath string) map[string]map[string]any {
	t.Helper()
	content, errorValue := os.ReadFile(profilePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	profiles := map[string]map[string]any{}
	if errorValue := json.Unmarshal(content, &profiles); errorValue != nil {
		t.Fatalf("%s is not a profile per document language: %v", profilePath, errorValue)
	}
	return profiles
}

func companyProfileAsked(profiles map[string]map[string]any, arguments any) map[string]any {
	encoded, _ := json.Marshal(arguments)
	var asked struct {
		Language string `json:"language"`
	}
	_ = json.Unmarshal(encoded, &asked)
	if profile, isKnown := profiles[strings.ToLower(strings.TrimSpace(asked.Language))]; isKnown {
		return profile
	}
	return profiles["ko"]
}

func companyInfoAnswer(profile map[string]any, keepsAnsweredFiles bool) (*sdkmcp.CallToolResult, error) {
	body := map[string]any{"result": profile}
	encodedBody, errorValue := json.Marshal(body)
	if errorValue != nil {
		return nil, errorValue
	}
	content := []sdkmcp.Content{&sdkmcp.TextContent{Text: string(encodedBody)}}
	if keepsAnsweredFiles {
		printed := map[string]any{"sealImage": "", "logoImage": ""}
		for key, value := range profile {
			printed[key] = value
		}
		encodedProfile, errorValue := json.MarshalIndent(printed, "", "  ")
		if errorValue != nil {
			return nil, errorValue
		}
		content = append(content, &sdkmcp.EmbeddedResource{Resource: &sdkmcp.ResourceContents{
			URI:      "internkim://files/" + officeExampleCompanyProfileName,
			MIMEType: "application/json",
			Text:     string(encodedProfile),
		}})
	}
	return &sdkmcp.CallToolResult{Content: content, StructuredContent: json.RawMessage(encodedBody)}, nil
}

func startCompanyRecordCatalog(t *testing.T, profilePath string) string {
	t.Helper()
	profiles := readCompanyProfiles(t, profilePath)
	descriptor := capabilityCatalogEntry(t, officeExampleCompanyInfoTool)
	description, _ := descriptor["description"].(string)
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "internkim", Version: "1"}, nil)
	server.AddTool(&sdkmcp.Tool{
		Name:        officeExampleCompanyInfoTool,
		Description: description,
		InputSchema: descriptor["inputSchema"],
		Meta:        sdkmcp.Meta{mcp.DescriptorMetaKey: descriptor},
	}, func(_ context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return companyInfoAnswer(companyProfileAsked(profiles, request.Params.Arguments), request.Params.Meta[mcp.AnsweredFilesMetaKey] == "kept")
	})
	httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return server },
		&sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	))
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

func writeOfficeExampleModels(t *testing.T, outputDirectory string) {
	t.Helper()
	document, _ := json.MarshalIndent(officeExampleModels{Primary: officeExamplePrimaryModel, Decision: officeExampleDecisionModel, VisualReview: officeExampleVisualReviewModel}, "", "  ")
	if errorValue := os.WriteFile(filepath.Join(outputDirectory, "models.json"), document, 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestOfficeExampleLive(t *testing.T) {
	requestPath := os.Getenv("OFFICE_EXAMPLE_REQUEST")
	outputDirectory := os.Getenv("OFFICE_EXAMPLE_OUTPUT")
	profilePath := os.Getenv("OFFICE_EXAMPLE_COMPANY_PROFILE")
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if requestPath == "" || outputDirectory == "" || profilePath == "" || apiKey == "" {
		t.Skip("OFFICE_EXAMPLE_REQUEST, OFFICE_EXAMPLE_OUTPUT, OFFICE_EXAMPLE_COMPANY_PROFILE and OPENROUTER_API_KEY are required")
	}
	request := readOfficeExampleRequest(t, requestPath)
	if errorValue := os.MkdirAll(filepath.Join(outputDirectory, "decision-images"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeOfficeExampleModels(t, outputDirectory)
	loopDecisions := decisions.Endpoint{URL: officeExampleDecisionsEndpoint, ModelName: officeExampleDecisionModel, APIKey: apiKey}.DecisionModel()
	scriptDecisions := decisions.Endpoint{URL: officeExampleDecisionsEndpoint, ModelName: officeExampleDecisionModel, APIKey: apiKey}.DecisionModel()
	visualDecisions := decisions.Endpoint{URL: officeExampleDecisionsEndpoint, ModelName: officeExampleVisualReviewModel, APIKey: apiKey}.DecisionModel()
	medium := officeExampleTierModel(t, apiKey, "medium")
	turn := VirtualTurn{
		Prompt:           request.Text,
		ConversationType: "direct",
		RouterTaskShape:  agentcontract.TaskShapeMaintenanceTask,
		InputAttachments: officeExampleAttachments(t, filepath.Dir(requestPath), request.Attachments),
		AllowedTaskStatuses: []task.TaskStatus{
			task.TaskStatusCompleted, task.TaskStatusWaitingApproval, task.TaskStatusWaitingUserInput, task.TaskStatusFailed, task.TaskStatusBlocked,
		},
	}
	scenario := VirtualSessionScenario{
		Name:                  "office_example_" + request.Name,
		ArtifactDirectoryPath: outputDirectory,
		LanguageModel:         officeExampleTierModel(t, apiKey, "low"),
		IntakeLanguageModel:   officeExampleTierModel(t, apiKey, "low"),
		XLowLanguageModel:     officeExampleTierModel(t, apiKey, "minimal"),
		LowLanguageModel:      officeExampleTierModel(t, apiKey, "low"),
		MediumLanguageModel:   medium,
		HighLanguageModel:     officeExampleTierModel(t, apiKey, "high"),
		XHighLanguageModel:    officeExampleTierModel(t, apiKey, "xhigh"),
		MaxLanguageModel:      officeExampleTierModel(t, apiKey, "max"),
		DisableScriptedModel:  true,
		UseLooseAssertions:    true,
		IsDeliveredOverACP:    true,
		RecordCatalogURL:      startCompanyRecordCatalog(t, profilePath),
		SkillDirectoryPaths:   fileDeliveryRouteSkillDirectories(),
		AllowedTools:          append(agentruntime.KernelToolNames(), officeExampleCompanyInfoTool),
		DecisionModel:         &decisionRecorder{label: "decision", delegate: loopDecisions, directory: outputDirectory},
		ConfigureToolCatalog: func(builder *agentruntime.ToolCatalogBuilder) {
			builder.UseScriptModels(
				&decisionRecorder{label: "script", delegate: scriptDecisions, directory: outputDirectory},
				&decisionRecorder{label: "visual", delegate: visualDecisions, directory: outputDirectory},
				languageRecorder{label: "generate", delegate: medium, directory: outputDirectory},
			)
		},
		Turns: []VirtualTurn{turn},
	}
	timeout := time.Duration(request.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 45 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := time.Now()
	result, runError := RunVirtualSession(ctx, scenario)
	preserveLiveSessionEvidence(t, outputDirectory, result, runError)
	t.Logf("office example %s finished in %.0fs, error %v", request.Name, time.Since(started).Seconds(), runError)
}
