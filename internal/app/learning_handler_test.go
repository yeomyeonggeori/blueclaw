package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/learning"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/pkg/memoryassertion"
)

func TestLearningHandlerRequiresSignedIdentityAndOwnAudience(t *testing.T) {
	root := t.TempDir()
	keyPath := filepath.Join(root, "assertion-key")
	if errorValue := os.WriteFile(keyPath, []byte("synthetic-key"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	store, errorValue := learning.Open(filepath.Join(root, "skills.json"), 20)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := store.Put(learning.Skill{ID: "other-skill", Audience: "person:other", Description: "Sample procedure", Instruction: "Sample steps", Status: "active"}); errorValue != nil {
		t.Fatal(errorValue)
	}
	directory := identityDirectory{identityService: identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail:        map[string]string{"admin@example.com": "admin", "member@example.com": "member"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{"admin": {PersonID: "admin", Circles: []string{policy.AdminCircleID}}, "member": {PersonID: "member"}},
	})}
	handler := learningHandlerForStore(store, directory, keyPath)
	for _, testCase := range []struct{ name, reader, body, signatureBody string }{
		{"unsigned", "", `{"id":"other-skill"}`, ""},
		{"member", "member", `{"id":"other-skill"}`, `{"id":"other-skill"}`},
		{"cross-audience-admin", "admin", `{"id":"other-skill"}`, `{"id":"other-skill"}`},
		{"tampered-body", "admin", `{"id":"other-skill"}`, `{"id":"own-skill"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/admin/api/agent-learning/skills/retire", strings.NewReader(testCase.body))
			if testCase.reader != "" {
				signLearningTestRequest(t, request, testCase.reader, testCase.signatureBody)
			}
			response := httptest.NewRecorder()
			handler.HandleMutation(response, request)
			if response.Code < 400 {
				t.Fatalf("unauthorized mutation succeeded: %d", response.Code)
			}
			if len(store.List("person:other", false)) != 1 {
				t.Fatal("another audience's skill was changed")
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/agent-learning/skills?audience=person:other", nil)
	signLearningTestRequest(t, request, "admin", "")
	response := httptest.NewRecorder()
	handler.HandleList(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "other-skill") {
		t.Fatalf("reader audience leaked: %d %s", response.Code, response.Body.String())
	}
}

func TestLearningSettingsHTTPPersistsInFreshNestedStore(t *testing.T) {
	root := t.TempDir()
	keyPath := filepath.Join(root, "assertion-key")
	if errorValue := os.WriteFile(keyPath, []byte("synthetic-key"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	storePath := filepath.Join(root, "nested", "learning", "skills.json")
	directory := identityDirectory{identityService: identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail:        map[string]string{"admin@example.com": "admin"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{"admin": {PersonID: "admin", Circles: []string{policy.AdminCircleID}}},
	})}
	store, errorValue := learning.Open(storePath, learning.DefaultActiveLimit)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	server := learningSettingsTestServer(t, learningHandlerForStore(store, directory, keyPath))
	initialStatus, initialSettings := learningSettingsRequest(t, server, http.MethodGet, "")
	if initialStatus != http.StatusOK || initialSettings.Enabled || initialSettings.ActiveLimit != 20 {
		t.Fatalf("initial settings response = %d %+v", initialStatus, initialSettings)
	}
	updatedSettings := `{"activeLimit":20,"enabled":true}`
	updatedStatus, updatedDocument := learningSettingsRequest(t, server, http.MethodPost, updatedSettings)
	if updatedStatus != http.StatusOK || !updatedDocument.Enabled || updatedDocument.ActiveLimit != 20 {
		t.Fatalf("updated settings response = %d %+v", updatedStatus, updatedDocument)
	}
	server.Close()
	settingsPath := storePath + ".settings"
	settingsInformation, errorValue := os.Stat(settingsPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if settingsInformation.Mode().Perm() != 0o600 {
		t.Fatalf("settings permissions = %o, want 600", settingsInformation.Mode().Perm())
	}
	parentInformation, errorValue := os.Stat(filepath.Dir(settingsPath))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if parentInformation.Mode().Perm() != 0o700 {
		t.Fatalf("settings parent permissions = %o, want 700", parentInformation.Mode().Perm())
	}
	restartedStore, errorValue := learning.Open(storePath, 1)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	restartedServer := learningSettingsTestServer(t, learningHandlerForStore(restartedStore, directory, keyPath))
	defer restartedServer.Close()
	persistedStatus, persistedSettings := learningSettingsRequest(t, restartedServer, http.MethodGet, "")
	if persistedStatus != http.StatusOK || !persistedSettings.Enabled || persistedSettings.ActiveLimit != 20 {
		t.Fatalf("restarted settings response = %d %+v", persistedStatus, persistedSettings)
	}
}

func learningSettingsTestServer(t *testing.T, handler adminapi.LearningHandler) *httptest.Server {
	t.Helper()
	multiplexer := http.NewServeMux()
	multiplexer.HandleFunc("/admin/api/agent-learning/settings", handler.HandleSettings)
	return httptest.NewServer(multiplexer)
}

func learningSettingsRequest(t *testing.T, server *httptest.Server, method, body string) (int, learning.Settings) {
	t.Helper()
	request, errorValue := http.NewRequest(method, server.URL+"/admin/api/agent-learning/settings", strings.NewReader(body))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	signLearningTestRequest(t, request, "admin", body)
	response, errorValue := server.Client().Do(request)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer response.Body.Close()
	var settings learning.Settings
	if errorValue := json.NewDecoder(response.Body).Decode(&settings); errorValue != nil {
		t.Fatal(errorValue)
	}
	return response.StatusCode, settings
}

func signLearningTestRequest(t *testing.T, request *http.Request, reader, body string) {
	signLearningTestRequestTarget(t, request, reader, body, request.URL.RequestURI())
}

func signLearningTestRequestTarget(t *testing.T, request *http.Request, reader, body, target string) {
	t.Helper()
	digest := sha256.Sum256([]byte(body))
	claims, errorValue := json.Marshal(struct {
		ReaderPersonID string `json:"readerPersonID"`
		ExpiresAt      int64  `json:"expiresAt"`
		BodySHA256     string `json:"bodySHA256"`
	}{reader, time.Now().Add(30 * time.Second).Unix(), hex.EncodeToString(digest[:])})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	encoded := base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte("synthetic-key"))
	mac.Write([]byte(request.Method + "\n" + target + "\n" + encoded))
	request.Header.Set(memoryassertion.HeaderName, encoded+"."+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

func TestPersonaRequestAuthorizerUsesExactPrincipalAndTarget(t *testing.T) {
	root := t.TempDir()
	keyPath := filepath.Join(root, "assertion-key")
	if errorValue := os.WriteFile(keyPath, []byte("synthetic-key"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	authorize := personaRequestAuthorizer(keyPath)
	for _, testCase := range []struct {
		name      string
		method    string
		target    string
		principal string
		valid     bool
	}{
		{name: "unsigned", method: http.MethodGet, target: "/admin/api/persona/user?personID=person-1", valid: false},
		{name: "own user", method: http.MethodGet, target: "/admin/api/persona/user?personID=person-1", principal: "person-1", valid: true},
		{name: "other user", method: http.MethodGet, target: "/admin/api/persona/user?personID=person-1", principal: "person-2", valid: false},
		{name: "seed cannot read user", method: http.MethodGet, target: "/admin/api/persona/user?personID=person-1", principal: "internkim-persona-seed", valid: false},
		{name: "seed", method: http.MethodPost, target: "/admin/api/persona/user?personID=person-1", principal: "internkim-persona-seed", valid: true},
		{name: "service cannot read user", method: http.MethodGet, target: "/admin/api/persona/user?personID=person-1", principal: "internkim-persona-service", valid: false},
		{name: "service cannot write user", method: http.MethodPut, target: "/admin/api/persona/user?personID=person-1", principal: "internkim-persona-service", valid: false},
		{name: "agent service", method: http.MethodGet, target: "/admin/api/persona/agent", principal: "internkim-persona-service", valid: true},
		{name: "seed cannot write agent", method: http.MethodPut, target: "/admin/api/persona/agent", principal: "internkim-persona-seed", valid: false},
		{name: "agent wrong principal", method: http.MethodGet, target: "/admin/api/persona/agent", principal: "person-1", valid: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(testCase.method, testCase.target, strings.NewReader("{}"))
			if testCase.principal != "" {
				signLearningTestRequestTarget(t, request, testCase.principal, "{}", request.URL.RequestURI())
			}
			if actual := authorize(request); actual != testCase.valid {
				t.Fatalf("authorization = %t, want %t", actual, testCase.valid)
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/persona/user?personID=person-1", strings.NewReader("{}"))
	signLearningTestRequestTarget(t, request, "person-1", "{}", request.URL.RequestURI())
	request.URL.RawQuery = "personID=person-2"
	if authorize(request) {
		t.Fatal("query target tampering succeeded")
	}
}
