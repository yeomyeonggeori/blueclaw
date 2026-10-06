package agentruntime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	ScriptHostURLEnvironmentName   = "BLUECLAW_SCRIPT_HOST_URL"
	ScriptHostTokenEnvironmentName = "BLUECLAW_SCRIPT_HOST_TOKEN"
	ScriptHostPath                 = "/harness/script-host"
	TaskEventScriptHostAnswered    = "script_host.answered"
	scriptHostRequestLimit         = 32 << 20
	scriptGenerationSchemaName     = "script_answer"
)

var errScriptHostModelMissing = errors.New("this host has no model for that question")

type ScriptHost struct {
	mutex  sync.RWMutex
	grants map[string]scriptGrant
}

type scriptGrant struct {
	ctx     context.Context
	builder *ToolCatalogBuilder
	request ToolCatalogRequest
}

type scriptGenerateRequest struct {
	System string                `json:"system"`
	Prompt string                `json:"prompt"`
	Images []model.DecisionImage `json:"images"`
	Schema json.RawMessage       `json:"schema"`
}

type scriptGenerateResponse struct {
	Answer json.RawMessage `json:"answer"`
	Model  string          `json:"model"`
	Usage  model.Usage     `json:"usage"`
}

type scriptToolResponse struct {
	Result  json.RawMessage   `json:"result,omitempty"`
	Content []json.RawMessage `json:"content,omitempty"`
	Files   []taskFile        `json:"files"`
	IsError bool              `json:"isError"`
}

type scriptHostAnswer struct {
	Route      string  `json:"route"`
	Model      string  `json:"model,omitempty"`
	CostUSD    float64 `json:"costUSD"`
	DurationMS int64   `json:"durationMs"`
	Error      string  `json:"error,omitempty"`
}

func NewScriptHost() *ScriptHost {
	return &ScriptHost{grants: map[string]scriptGrant{}}
}

func (host *ScriptHost) Handler() http.Handler {
	multiplexer := http.NewServeMux()
	multiplexer.HandleFunc("POST /decide", host.granted(handleScriptDecide))
	multiplexer.HandleFunc("POST /generate", host.granted(handleScriptGenerate))
	multiplexer.HandleFunc("POST /tools/{toolName}", host.granted(handleScriptTool))
	return multiplexer
}

func (host *ScriptHost) grant(grant scriptGrant) string {
	token := newScriptHostToken()
	host.mutex.Lock()
	defer host.mutex.Unlock()
	host.grants[token] = grant
	return token
}

func (host *ScriptHost) revoke(token string) {
	host.mutex.Lock()
	defer host.mutex.Unlock()
	delete(host.grants, token)
}

func (host *ScriptHost) grantFor(request *http.Request) (scriptGrant, bool) {
	token := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	grant, isGranted := host.grants[token]
	return grant, isGranted && token != ""
}

func newScriptHostToken() string {
	token := make([]byte, 24)
	_, _ = rand.Read(token)
	return hex.EncodeToString(token)
}

type scriptHandler func(context.Context, scriptGrant, *http.Request) (any, scriptHostAnswer, error)

func (host *ScriptHost) granted(handle scriptHandler) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		grant, isGranted := host.grantFor(request)
		if !isGranted {
			writeScriptHostError(responseWriter, http.StatusUnauthorized, errors.New("this token belongs to no running command"))
			return
		}
		ctx, release := grant.contextFor(request.Context())
		defer release()
		request.Body = http.MaxBytesReader(responseWriter, request.Body, scriptHostRequestLimit)
		startedAt := time.Now()
		response, answer, errorValue := handle(ctx, grant, request)
		answer.DurationMS = time.Since(startedAt).Milliseconds()
		grant.recordAnswer(answer, errorValue)
		if errorValue != nil {
			writeScriptHostError(responseWriter, scriptHostStatus(errorValue), errorValue)
			return
		}
		writeScriptHostJSON(responseWriter, http.StatusOK, response)
	}
}

func (grant scriptGrant) contextFor(requestContext context.Context) (context.Context, func()) {
	merged, cancel := context.WithCancel(context.WithoutCancel(grant.ctx))
	stopWithRequest := context.AfterFunc(requestContext, cancel)
	stopWithCommand := context.AfterFunc(grant.ctx, cancel)
	return merged, func() {
		stopWithRequest()
		stopWithCommand()
		cancel()
	}
}

func (grant scriptGrant) recordAnswer(answer scriptHostAnswer, errorValue error) {
	taskRunID := toolcontract.TaskRunIDFromContext(grant.ctx)
	if grant.builder.taskRunService == nil || taskRunID == "" {
		return
	}
	if errorValue != nil {
		answer.Error = errorValue.Error()
	}
	grant.builder.taskRunService.AppendTaskEvent(taskRunID, TaskEventScriptHostAnswered, MarshalBody(answer))
}

func handleScriptDecide(ctx context.Context, grant scriptGrant, request *http.Request) (any, scriptHostAnswer, error) {
	answer := scriptHostAnswer{Route: "decide"}
	var decision model.DecisionRequest
	if errorValue := decodeScriptRequest(request.Body, &decision); errorValue != nil {
		return nil, answer, errorValue
	}
	if len(decision.Questions) == 0 {
		return nil, answer, scriptRequestError("a decision carries at least one question")
	}
	decisionModel := grant.builder.scriptDecisionModelFor(decision)
	if decisionModel == nil {
		return nil, answer, errScriptHostModelMissing
	}
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{State: decision.State, Questions: decision.Questions, Images: decision.Images})
	answer.Model, answer.CostUSD = response.ModelName, response.Usage.CostUSD
	return response, answer, errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) scriptDecisionModelFor(decision model.DecisionRequest) model.DecisionModel {
	if len(decision.Images) > 0 {
		return toolCatalogBuilder.scriptImageDecisionModel
	}
	return toolCatalogBuilder.scriptDecisionModel
}

func handleScriptGenerate(ctx context.Context, grant scriptGrant, request *http.Request) (any, scriptHostAnswer, error) {
	answer := scriptHostAnswer{Route: "generate"}
	var generation scriptGenerateRequest
	if errorValue := decodeScriptRequest(request.Body, &generation); errorValue != nil {
		return nil, answer, errorValue
	}
	if strings.TrimSpace(generation.Prompt) == "" || !json.Valid(generation.Schema) {
		return nil, answer, scriptRequestError("a generation carries a prompt and a JSON schema")
	}
	if grant.builder.scriptGenerationModel == nil {
		return nil, answer, errScriptHostModelMissing
	}
	response, errorValue := grant.builder.scriptGenerationModel.GenerateStructuredResponse(ctx, generation.structuredRequest())
	answer.Model, answer.CostUSD = response.ModelName, response.Usage.CostUSD
	if errorValue != nil {
		return nil, answer, errorValue
	}
	if !json.Valid([]byte(response.Content)) {
		return nil, answer, errors.New("the model answered text that is not JSON")
	}
	return scriptGenerateResponse{Answer: json.RawMessage(response.Content), Model: response.ModelName, Usage: response.Usage}, answer, nil
}

func (generation scriptGenerateRequest) structuredRequest() model.StructuredResponseRequest {
	parts := []model.MessagePart{{Type: "text", Text: generation.Prompt}}
	for _, image := range generation.Images {
		parts = append(parts, model.MessagePart{Type: "image", MimeType: image.MediaType, DataBase64: base64.StdEncoding.EncodeToString(image.Data)})
	}
	messages := []model.Message{}
	if system := strings.TrimSpace(generation.System); system != "" {
		messages = append(messages, model.Message{Role: "system", Content: system})
	}
	messages = append(messages, model.Message{Role: "user", Parts: parts})
	return model.StructuredResponseRequest{
		Messages:               messages,
		StructuredOutputSchema: model.StructuredOutputSchema{Name: scriptGenerationSchemaName, Document: string(generation.Schema), IsStrictlyEnforced: true},
	}
}

func handleScriptTool(ctx context.Context, grant scriptGrant, request *http.Request) (any, scriptHostAnswer, error) {
	toolName := strings.TrimSpace(request.PathValue("toolName"))
	answer := scriptHostAnswer{Route: "tools/" + toolName}
	input, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return nil, answer, scriptRequestError(errorValue.Error())
	}
	result, errorValue := grant.builder.callRecordToolAsRequester(ctx, grant.request, toolName, compactJSONOrEmptyObject(input))
	if errorValue != nil {
		return nil, answer, errorValue
	}
	return scriptToolResponse{Result: resultInsideTheEnvelope(result.StructuredContent), Content: result.Content, Files: keptFiles(result.Content), IsError: result.IsError}, answer, nil
}

type scriptRequestError string

func (requestError scriptRequestError) Error() string {
	return string(requestError)
}

func decodeScriptRequest(body io.Reader, target any) error {
	if errorValue := json.NewDecoder(body).Decode(target); errorValue != nil {
		return scriptRequestError("the request is not the JSON this route takes: " + errorValue.Error())
	}
	return nil
}

func scriptHostStatus(errorValue error) int {
	var requestError scriptRequestError
	switch {
	case errors.As(errorValue, &requestError):
		return http.StatusBadRequest
	case errors.Is(errorValue, errScriptHostModelMissing):
		return http.StatusNotImplemented
	case errors.Is(errorValue, errRecordToolUnavailable):
		return http.StatusForbidden
	}
	return http.StatusBadGateway
}

func writeScriptHostError(responseWriter http.ResponseWriter, status int, errorValue error) {
	writeScriptHostJSON(responseWriter, status, map[string]string{"error": errorValue.Error()})
}

func writeScriptHostJSON(responseWriter http.ResponseWriter, status int, document any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(status)
	_ = json.NewEncoder(responseWriter).Encode(document)
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseScriptHost(host *ScriptHost, url string) {
	toolCatalogBuilder.scriptHost = host
	toolCatalogBuilder.scriptHostURL = strings.TrimRight(strings.TrimSpace(url), "/")
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseScriptModels(decisionModel model.DecisionModel, imageDecisionModel model.DecisionModel, generationModel model.LanguageModelProvider) {
	toolCatalogBuilder.scriptDecisionModel = decisionModel
	toolCatalogBuilder.scriptImageDecisionModel = imageDecisionModel
	toolCatalogBuilder.scriptGenerationModel = generationModel
}

func (toolCatalogBuilder *ToolCatalogBuilder) grantScriptHost(ctx context.Context, request ToolCatalogRequest) (map[string]string, func()) {
	if toolCatalogBuilder.scriptHost == nil || toolCatalogBuilder.scriptHostURL == "" {
		return map[string]string{}, func() {}
	}
	token := toolCatalogBuilder.scriptHost.grant(scriptGrant{ctx: ctx, builder: toolCatalogBuilder, request: request})
	environment := map[string]string{ScriptHostURLEnvironmentName: toolCatalogBuilder.scriptHostURL, ScriptHostTokenEnvironmentName: token}
	return environment, func() { toolCatalogBuilder.scriptHost.revoke(token) }
}
