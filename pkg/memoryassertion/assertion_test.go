package memoryassertion

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVerifierAcceptsTheAdmindFixture(t *testing.T) {
	fixtureBytes, errorValue := os.ReadFile("testdata/assertion.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var fixture struct {
		Secret    string `json:"secret"`
		Method    string `json:"method"`
		Path      string `json:"path"`
		ExpiresAt int64  `json:"expiresAt"`
		Body      string `json:"body"`
		Header    string `json:"header"`
	}
	if errorValue := json.Unmarshal(fixtureBytes, &fixture); errorValue != nil {
		t.Fatal(errorValue)
	}
	request := httptest.NewRequest(fixture.Method, "http://localhost"+fixture.Path, strings.NewReader(fixture.Body))
	request.Header.Set(HeaderName, fixture.Header)
	verifier := New([]byte(fixture.Secret))
	verifier.clock = func() time.Time { return time.Unix(fixture.ExpiresAt-30, 0) }
	readerPersonID, errorValue := verifier.Verify(request, []byte(fixture.Body))
	if errorValue != nil || readerPersonID != "user:person-1" {
		t.Fatalf("fixture verification failed for %q: %v", readerPersonID, errorValue)
	}
}

func TestVerifierAcceptsBoundAssertionAndRejectsTampering(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"namespaceID":"user:one","factID":"fact:1"}`)
	request := httptest.NewRequest("POST", "http://localhost/admin/api/memory/facts/delete", strings.NewReader(string(body)))
	request.Header.Set(HeaderName, signAssertion(secret, "person-1", time.Now().Add(20*time.Second).Unix(), body, "POST", request.URL.Path))
	verifier := New(secret)
	readerPersonID, errorValue := verifier.Verify(request, body)
	if errorValue != nil || readerPersonID != "person-1" {
		t.Fatalf("expected valid assertion, got %q: %v", readerPersonID, errorValue)
	}
	request.Header.Set(HeaderName, signAssertion(secret, "person-1", time.Now().Add(20*time.Second).Unix(), []byte("tampered"), "POST", request.URL.Path))
	if _, errorValue = verifier.Verify(request, body); errorValue == nil {
		t.Fatal("expected body tampering to fail")
	}
	request.Header.Set(HeaderName, signAssertion(secret, "other-person", time.Now().Add(20*time.Second).Unix(), body, "POST", request.URL.Path))
	parts := strings.Split(request.Header.Get(HeaderName), ".")
	documentBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var assertion document
	_ = json.Unmarshal(documentBytes, &assertion)
	assertion.ReaderPersonID = "tampered-person"
	tamperedDocument, _ := json.Marshal(assertion)
	request.Header.Set(HeaderName, base64.RawURLEncoding.EncodeToString(tamperedDocument)+"."+parts[1])
	if _, errorValue = verifier.Verify(request, body); errorValue == nil {
		t.Fatal("expected signed reader identity tampering to fail")
	}
}

func TestVerifierRejectsExpiredAndWrongEndpoint(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte("body")
	request := httptest.NewRequest("POST", "http://localhost/admin/api/memory/facts/delete", strings.NewReader(string(body)))
	request.Header.Set(HeaderName, signAssertion(secret, "person-1", time.Now().Add(-time.Second).Unix(), body, "POST", request.URL.Path))
	if _, errorValue := New(secret).Verify(request, body); errorValue == nil {
		t.Fatal("expected expired assertion to fail")
	}
	request.Header.Set(HeaderName, signAssertion(secret, "person-1", time.Now().Add(20*time.Second).Unix(), body, "POST", "/admin/api/memory/facts/update"))
	if _, errorValue := New(secret).Verify(request, body); errorValue == nil {
		t.Fatal("expected wrong endpoint to fail")
	}
	request.Header.Set(HeaderName, signAssertion(secret, "person-1", time.Now().Add(20*time.Second).Unix(), body, "POST", request.URL.Path))
	parts := strings.Split(request.Header.Get(HeaderName), ".")
	documentBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var assertion document
	_ = json.Unmarshal(documentBytes, &assertion)
	assertion.ExpiresAt = time.Now().Add(45 * time.Second).Unix()
	tamperedDocument, _ := json.Marshal(assertion)
	request.Header.Set(HeaderName, base64.RawURLEncoding.EncodeToString(tamperedDocument)+"."+parts[1])
	if _, errorValue := New(secret).Verify(request, body); errorValue == nil {
		t.Fatal("expected signed expiry tampering to fail")
	}
}

func signAssertion(secret []byte, readerPersonID string, expiresAt int64, body []byte, method string, path string) string {
	digest := sha256.Sum256(body)
	document, _ := json.Marshal(document{ReaderPersonID: readerPersonID, ExpiresAt: expiresAt, BodySHA256: hex.EncodeToString(digest[:])})
	payload := base64.RawURLEncoding.EncodeToString(document)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(method + "\n" + path + "\n" + payload))
	return base64.RawURLEncoding.EncodeToString(document) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
