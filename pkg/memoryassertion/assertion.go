package memoryassertion

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const HeaderName = "X-Blueclaw-Memory-Assertion"

type document struct {
	ReaderPersonID string `json:"readerPersonID"`
	ExpiresAt      int64  `json:"expiresAt"`
	BodySHA256     string `json:"bodySHA256"`
}

type Verifier struct {
	secret []byte
	clock  func() time.Time
}

func New(secret []byte) Verifier {
	return Verifier{secret: append([]byte{}, secret...), clock: time.Now}
}

func (verifier Verifier) Verify(request *http.Request, body []byte) (string, error) {
	return verifier.verify(request, body, request.URL.Path)
}

func (verifier Verifier) VerifyRequestTarget(request *http.Request, body []byte) (string, error) {
	return verifier.verify(request, body, request.URL.RequestURI())
}

func (verifier Verifier) verify(request *http.Request, body []byte, requestTarget string) (string, error) {
	if len(verifier.secret) == 0 || len(body) > 16*1024 {
		return "", errors.New("memory assertion unavailable")
	}
	parts := strings.Split(request.Header.Get(HeaderName), ".")
	if len(parts) != 2 {
		return "", errors.New("memory assertion missing")
	}
	documentBytes, errorValue := base64.RawURLEncoding.DecodeString(parts[0])
	if errorValue != nil {
		return "", errors.New("memory assertion invalid")
	}
	var assertion document
	if json.Unmarshal(documentBytes, &assertion) != nil || assertion.ReaderPersonID == "" {
		return "", errors.New("memory assertion invalid")
	}
	now := verifier.clock().Unix()
	if assertion.ExpiresAt <= now || assertion.ExpiresAt > now+60 {
		return "", errors.New("memory assertion expired")
	}
	digest := sha256.Sum256(body)
	if !hmac.Equal([]byte(strings.ToLower(assertion.BodySHA256)), []byte(hex.EncodeToString(digest[:]))) {
		return "", errors.New("memory assertion body mismatch")
	}
	mac := hmac.New(sha256.New, verifier.secret)
	mac.Write([]byte(request.Method + "\n" + requestTarget + "\n" + parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	provided, errorValue := base64.RawURLEncoding.DecodeString(parts[1])
	if errorValue != nil || !hmac.Equal([]byte(expected), []byte(parts[1])) || len(provided) == 0 {
		return "", errors.New("memory assertion signature mismatch")
	}
	return assertion.ReaderPersonID, nil
}
