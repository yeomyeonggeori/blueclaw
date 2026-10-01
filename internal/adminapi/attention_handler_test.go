package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/llm"
)

func TestAttentionHandlerReturnsSilentDecision(t *testing.T) {
	languageModel := &recordingAttentionLanguageModel{content: `{"status":"ATTENTION_SILENT","message":"","reason":"not_useful"}`}
	handler := AttentionHandler{LanguageModel: languageModel}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/attention/run", strings.NewReader(`{
		"jobID":"job-1",
		"toolName":"user_confirm",
		"localDecision":{
			"shouldEscalate":true,
			"importance":"medium",
			"confidence":0.9,
			"reasonCodes":["blocked"],
			"summaryForRemote":"Waiting for confirmation.",
			"privacyClass":"user_input"
		}
	}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunAttention(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), "ATTENTION_SILENT") {
		t.Fatalf("expected silent response, got %s", responseRecorder.Body.String())
	}
	if languageModel.request.StructuredOutputSchema.Name != "blueclaw_attention_decision" {
		t.Fatalf("expected attention schema, got %+v", languageModel.request.StructuredOutputSchema)
	}
}

func TestAttentionHandlerReturnsMessageDecision(t *testing.T) {
	handler := AttentionHandler{LanguageModel: &recordingAttentionLanguageModel{content: `{"status":"ATTENTION_MESSAGE","message":"This looks like it needs a check.","reason":"blocked"}`}}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/attention/run", strings.NewReader(`{
		"jobID":"job-1",
		"toolName":"browser_wait",
		"localDecision":{
			"shouldEscalate":true,
			"importance":"high",
			"confidence":0.95,
			"reasonCodes":["blocked"],
			"summaryForRemote":"The page is still loading.",
			"privacyClass":"device_browser"
		}
	}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunAttention(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), "ATTENTION_MESSAGE") || !strings.Contains(responseRecorder.Body.String(), "This looks like it needs a check.") {
		t.Fatalf("expected message response, got %s", responseRecorder.Body.String())
	}
}

func TestAttentionHandlerRejectsSilentLocalDecision(t *testing.T) {
	handler := AttentionHandler{LanguageModel: &recordingAttentionLanguageModel{content: `{"status":"ATTENTION_SILENT","message":"","reason":"not_useful"}`}}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/attention/run", strings.NewReader(`{
		"jobID":"job-1",
		"toolName":"user_confirm",
		"localDecision":{
			"shouldEscalate":false,
			"summaryForRemote":"Waiting for confirmation."
		}
	}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunAttention(responseRecorder, request)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

type recordingAttentionLanguageModel struct {
	content string
	request llm.StructuredResponseRequest
}

func (languageModel *recordingAttentionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *recordingAttentionLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.request = request
	return llm.StructuredResponse{Content: languageModel.content}, nil
}
