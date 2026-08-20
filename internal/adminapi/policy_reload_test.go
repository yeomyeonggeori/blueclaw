package adminapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func deliveredPolicyFile(t *testing.T, document string) string {
	t.Helper()
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if errorValue := os.WriteFile(policyPath, []byte(document), 0o600); errorValue != nil {
		t.Fatalf("write policy: %v", errorValue)
	}
	return policyPath
}

func reloadRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/admin/api/policy/reload", nil)
}

func TestAReloadMakesTheDeliveredRosterCurrent(t *testing.T) {
	watcher := &policy.PolicyWatcher{}
	policyPath := deliveredPolicyFile(t, `{"people":[{"personID":"person-1","emails":["이샘플@example.com"]},{"personID":"person-2","emails":["박예시@example.com"]}]}`)
	handler := PolicyHandler{PolicyPath: policyPath, PolicyWatcher: watcher}

	recorder := httptest.NewRecorder()
	handler.HandleReloadPolicy(recorder, reloadRequest())

	if recorder.Code != http.StatusOK {
		t.Fatalf("a roster the host delivered has to become readable, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if len(watcher.CurrentPolicyDocument().People) != 2 {
		t.Fatal("an agent that answered the reload and kept its old roster is the outage this exists to end")
	}
}

func TestAReloadThatCannotReadTheFileLeavesTheRosterAlone(t *testing.T) {
	watcher := &policy.PolicyWatcher{}
	watcher.ReloadPolicyDocument(policy.PolicyDocument{People: []policy.PersonPolicy{{PersonID: "person-1"}}})
	handler := PolicyHandler{PolicyPath: filepath.Join(t.TempDir(), "absent.json"), PolicyWatcher: watcher}

	recorder := httptest.NewRecorder()
	handler.HandleReloadPolicy(recorder, reloadRequest())

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("a file that cannot be read is a failure the caller has to see, got %d", recorder.Code)
	}
	if len(watcher.CurrentPolicyDocument().People) != 1 {
		t.Fatal("a failed reload must leave the roster it already had, because emptying it refuses everyone")
	}
}

func TestAReloadReadsAgainWhenItCatchesTheFileMidWrite(t *testing.T) {
	watcher := &policy.PolicyWatcher{}
	policyPath := deliveredPolicyFile(t, `{"people":[{"personID":"person-1","emails":["이샘플@example.com"]}`)
	handler := PolicyHandler{PolicyPath: policyPath, PolicyWatcher: watcher}
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = os.WriteFile(policyPath, []byte(`{"people":[{"personID":"person-1","emails":["이샘플@example.com"]}]}`), 0o600)
	}()

	recorder := httptest.NewRecorder()
	handler.HandleReloadPolicy(recorder, reloadRequest())

	if recorder.Code != http.StatusOK {
		t.Fatalf("a file caught mid-write settles, and refusing on that window leaves the agent on its old roster: %d %s", recorder.Code, recorder.Body.String())
	}
	if len(watcher.CurrentPolicyDocument().People) != 1 {
		t.Fatal("the second read has to be the one that lands")
	}
}
