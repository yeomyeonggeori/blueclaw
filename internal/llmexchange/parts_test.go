package llmexchange

import (
	"strings"
	"testing"
)

func chatRequest(lastMessage string) string {
	systemPrompt := strings.Repeat("You are the company's agent. ", 40)
	return `{"model":"z-ai/glm-5.3","seed":1234567,"temperature":0.20,
  "messages":[{"role":"system","content":"` + systemPrompt + `"},{"role":"user","content":"` + lastMessage + `"}],
  "response_format":{"type":"json_schema","json_schema":{"schema":{"type":"object","properties":{"zeta":{"type":"string","description":"` + strings.Repeat("z", 300) + `"},"alpha":{"type":"string","description":"` + strings.Repeat("a", 300) + `"}}}}}}`
}

func storedLookup(parts []Part) PartLookup {
	bodies := map[string]string{}
	for _, part := range parts {
		bodies[part.Hash] = part.Body
	}
	return func(hashes []string) (map[string]string, error) {
		found := map[string]string{}
		for _, hash := range hashes {
			if body, isStored := bodies[hash]; isStored {
				found[hash] = body
			}
		}
		return found, nil
	}
}

func splitAndJoin(t *testing.T, document string) (string, []Part) {
	t.Helper()
	rootHash, parts, errorValue := Split(document)
	if errorValue != nil {
		t.Fatalf("expected the document to split: %v", errorValue)
	}
	joined, errorValue := Join(rootHash, storedLookup(parts))
	if errorValue != nil {
		t.Fatalf("expected the parts to join: %v", errorValue)
	}
	return joined, parts
}

func TestJoinGivesBackTheSplitBytesExactly(t *testing.T) {
	document := chatRequest(`회의록 \"정리\" <해줘> & 😀`)

	joined, parts := splitAndJoin(t, document)

	if joined != document {
		t.Fatalf("expected the very bytes that were split, key order and whitespace included\njoined %s\nsplit  %s", joined, document)
	}
	if len(parts) < 3 {
		t.Fatalf("expected the long prompt and the schema to be parts of their own, got %d parts", len(parts))
	}
}

func TestALongStringIsAPartOfItsOwn(t *testing.T) {
	image := strings.Repeat("iVBORw0KGgo", 200)
	document := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + image + `"}}]}]}`

	joined, parts := splitAndJoin(t, document)

	if joined != document {
		t.Fatal("expected the image to come back unchanged")
	}
	for _, part := range parts {
		if strings.HasPrefix(part.Body, `"data:image/png`) {
			return
		}
	}
	t.Fatalf("expected the image string stored as its own part, got %d parts", len(parts))
}

func TestTwoRequestsStoreTheirSharedPromptOnce(t *testing.T) {
	_, firstParts, _ := Split(chatRequest("first"))
	_, secondParts, _ := Split(chatRequest("second"))

	firstHashes := map[string]bool{}
	for _, part := range firstParts {
		firstHashes[part.Hash] = true
	}
	sharedCount := 0
	for _, part := range secondParts {
		if firstHashes[part.Hash] {
			sharedCount++
		}
	}
	if sharedCount != len(secondParts)-1 {
		t.Fatalf("expected every part but the root, which holds the new message, to be shared, got %d shared of %d", sharedCount, len(secondParts))
	}
}

func TestASmallDocumentIsOnePart(t *testing.T) {
	_, parts, _ := Split(`{"prompt":"hello"}`)

	if len(parts) != 1 || parts[0].Body != `{"prompt":"hello"}` {
		t.Fatalf("expected a small document to be stored whole, got %+v", parts)
	}
}

func TestADocumentThatIsNotJSONIsKeptWhole(t *testing.T) {
	stream := "data: {\"choices\":[]}\n\n" + strings.Repeat("data: {}\n\n", 100) + "data: [DONE]\n\n"

	joined, parts := splitAndJoin(t, stream)

	if joined != stream || len(parts) != 1 {
		t.Fatalf("expected one part holding the whole stream, got %d parts", len(parts))
	}
}

func TestADocumentHoldingAPartReferenceIsRefused(t *testing.T) {
	_, _, errorValue := Split(`{"a":{"$part":"` + strings.Repeat("0", 64) + `"}}`)

	if errorValue == nil {
		t.Fatal("expected a document that could not be joined back unambiguously to be refused")
	}
}

func TestJoinSaysWhichPartIsMissing(t *testing.T) {
	rootHash, parts, _ := Split(chatRequest("hello"))

	_, errorValue := Join(rootHash, storedLookup(parts[1:]))

	if errorValue == nil || !strings.Contains(errorValue.Error(), "is not stored") {
		t.Fatalf("expected a missing part to be named, got %v", errorValue)
	}
}
