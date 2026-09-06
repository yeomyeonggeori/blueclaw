package app

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/learning"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/pkg/memoryassertion"
)

func openLearningStore(root string) (*learning.Store, error) {
	if root == "" {
		return nil, nil
	}
	return learning.Open(filepath.Join(root, ".blueclaw", "state", "learning", "skills.json"), learning.DefaultActiveLimit)
}

func learningHandlerForStore(store *learning.Store, directory identityDirectory, keyPath string, soulReaders ...adminapi.SoulLearningReader) adminapi.LearningHandler {
	handler := adminapi.LearningHandler{
		Store:          store,
		ReaderPersonID: signedLearningReader(keyPath),
		ReaderAudience: func(personID string) string {
			if directory.identityService.ResolvePersonPrimaryEmail(personID) == "" {
				return ""
			}
			return "person:" + personID
		},
		IsAdministrator: func(personID string) bool {
			return slices.Contains(directory.identityService.ResolvePersonAccess(personID).Circles, policy.AdminCircleID)
		},
	}
	if len(soulReaders) > 0 {
		handler.Soul = soulReaders[0]
	}
	return handler
}

func signedLearningReader(keyPath string) func(*http.Request) string {
	return signedReader(keyPath, true)
}

func signedPersonaReader(keyPath string) func(*http.Request) string {
	return signedReader(keyPath, true)
}

func signedReader(keyPath string, bindRequestTarget bool) func(*http.Request) string {
	return func(request *http.Request) string {
		var body []byte
		if request.Body != nil {
			var errorValue error
			body, errorValue = io.ReadAll(io.LimitReader(request.Body, 16385))
			request.Body = io.NopCloser(bytes.NewReader(body))
			if errorValue != nil || len(body) > 16384 {
				return ""
			}
		}
		secret, readError := os.ReadFile(keyPath)
		if readError != nil {
			return ""
		}
		verifier := memoryassertion.New(bytes.TrimSpace(secret))
		var personID string
		var errorValue error
		if bindRequestTarget {
			personID, errorValue = verifier.VerifyRequestTarget(request, body)
		} else {
			personID, errorValue = verifier.Verify(request, body)
		}
		if errorValue != nil {
			return ""
		}
		return personID
	}
}
