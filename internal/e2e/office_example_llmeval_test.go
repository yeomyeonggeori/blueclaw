//go:build appliance && llmeval

package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const (
	officeExampleModelEndpoint     = "https://openrouter.ai/api/v1"
	officeExampleDecisionsEndpoint = "https://openrouter.ai/api/alpha/decisions"
	officeExamplePrimaryModel      = "z-ai/glm-5.3-flash"
	officeExampleDecisionModel     = "~typesafe/jev-latest"
	officeExampleVisualReviewModel = "cloudflare/clef"
	officeExampleProviderSort      = "throughput"
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
	dataRoomPath := os.Getenv("OFFICE_EXAMPLE_DATA_ROOM")
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
	claimDecisions := decisions.Endpoint{URL: officeExampleDecisionsEndpoint, ModelName: officeExampleDecisionModel, APIKey: apiKey}.DecisionModel()
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
		RecordCatalogURL:      startCompanyRecordCatalog(t, profilePath, dataRoomPath),
		SkillDirectoryPaths:   fileDeliveryRouteSkillDirectories(),
		AllowedTools:          append(agentruntime.KernelToolNames(), companyRecordToolNames...),
		DecisionModel:         &decisionRecorder{label: "decision", delegate: loopDecisions, directory: outputDirectory},
		ConfigureToolCatalog: func(builder *agentruntime.ToolCatalogBuilder) {
			builder.UseClaimDecisionModel(&decisionRecorder{label: "claim", delegate: claimDecisions, directory: outputDirectory})
			builder.UseVisualReviewModels(&decisionRecorder{label: "visual", delegate: visualDecisions, directory: outputDirectory}, languageRecorder{label: "fixer", delegate: medium, directory: outputDirectory})
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
