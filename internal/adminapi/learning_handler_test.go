package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/learning"
)

type testSoulReader struct{ revision learning.SoulRevision }

func (reader testSoulReader) CurrentSoul(context.Context) (learning.SoulRevision, error) {
	return reader.revision, nil
}
func (reader testSoulReader) SoulHistory(context.Context) ([]learning.SoulRevision, error) {
	return []learning.SoulRevision{reader.revision}, nil
}

func TestLearningHandlerDeniesUnsignedReader(t *testing.T) {
	store, errorValue := learning.Open(filepath.Join(t.TempDir(), "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := LearningHandler{Store: store, ReaderPersonID: func(*http.Request) string { return "" }, ReaderAudience: func(string) string { return "company" }}
	recorder := httptest.NewRecorder()
	handler.HandleList(recorder, httptest.NewRequest("GET", "/admin/api/agent-learning/skills", nil))
	if recorder.Code != 403 {
		t.Fatalf("expected unsigned reader denial, got %d", recorder.Code)
	}
}

func TestLearningHandlerRedactsSoulEvidenceAndDeniesRestore(t *testing.T) {
	revision := learning.SoulRevision{Version: 2, EvidenceIDs: []string{"private-task"}}
	handler := LearningHandler{Soul: testSoulReader{revision: revision}, ReaderPersonID: func(*http.Request) string { return "person-1" }, ReaderAudience: func(string) string { return "company" }, IsAdministrator: func(string) bool { return true }}
	recorder := httptest.NewRecorder()
	handler.HandleSoul(recorder, httptest.NewRequest("GET", "/admin/api/agent-learning/soul", nil))
	var response learning.SoulRevision
	if json.NewDecoder(recorder.Body).Decode(&response) != nil || len(response.EvidenceIDs) != 0 {
		t.Fatalf("soul evidence was exposed: %s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.HandleSoul(recorder, httptest.NewRequest("POST", "/admin/api/agent-learning/soul/restore", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("soul restore must not be exposed, got %d", recorder.Code)
	}
}
