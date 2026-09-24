package llmexchange

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const smallestSeparatePartBytes = 512

var partReferencePattern = regexp.MustCompile(`\{"\$part":"([0-9a-f]{64})"\}`)

type Exchange struct {
	Request  string `json:"request,omitempty"`
	Response string `json:"response,omitempty"`
	Input    string `json:"input,omitempty"`
}

type Part struct {
	Hash string
	Body string
}

type PartLookup func(hashes []string) (map[string]string, error)

func Split(document string) (string, []Part, error) {
	if partReferencePattern.MatchString(document) {
		return "", nil, errors.New("the document already holds a part reference, so its parts could not be told apart from it")
	}
	splitter := partSplitter{document: document, seen: map[string]bool{}}
	if !json.Valid([]byte(document)) {
		return splitter.keep(document), splitter.parts, nil
	}
	rootStart := skipSpace(document, 0)
	rootEnd, rewrittenRoot := splitter.value(rootStart)
	return splitter.keep(document[:rootStart] + rewrittenRoot + document[rootEnd:]), splitter.parts, nil
}

type partSplitter struct {
	document string
	parts    []Part
	seen     map[string]bool
}

func (splitter *partSplitter) value(start int) (int, string) {
	switch splitter.document[start] {
	case '{', '[':
		return splitter.composite(start)
	case '"':
		end := stringEnd(splitter.document, start)
		return end, splitter.document[start:end]
	default:
		end := literalEnd(splitter.document, start)
		return end, splitter.document[start:end]
	}
}

func (splitter *partSplitter) composite(start int) (int, string) {
	document := splitter.document
	isObject := document[start] == '{'
	rewritten := strings.Builder{}
	copiedUpTo := start
	cursor := skipSpace(document, start+1)
	for document[cursor] != '}' && document[cursor] != ']' {
		if isObject {
			cursor = skipSpace(document, stringEnd(document, cursor))
			cursor = skipSpace(document, cursor+1)
		}
		childEnd, rewrittenChild := splitter.value(cursor)
		rewritten.WriteString(document[copiedUpTo:cursor])
		rewritten.WriteString(splitter.separateIfLarge(rewrittenChild))
		copiedUpTo = childEnd
		cursor = skipSpace(document, childEnd)
		if document[cursor] == ',' {
			cursor = skipSpace(document, cursor+1)
		}
	}
	rewritten.WriteString(document[copiedUpTo : cursor+1])
	return cursor + 1, rewritten.String()
}

func (splitter *partSplitter) separateIfLarge(body string) string {
	if len(body) < smallestSeparatePartBytes {
		return body
	}
	return Reference(splitter.keep(body))
}

func (splitter *partSplitter) keep(body string) string {
	digest := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(digest[:])
	if !splitter.seen[hash] {
		splitter.seen[hash] = true
		splitter.parts = append(splitter.parts, Part{Hash: hash, Body: body})
	}
	return hash
}

func Reference(hash string) string {
	return `{"$part":"` + hash + `"}`
}

func skipSpace(document string, index int) int {
	for index < len(document) && strings.IndexByte(" \t\n\r", document[index]) >= 0 {
		index++
	}
	return index
}

func stringEnd(document string, start int) int {
	for index := start + 1; index < len(document); index++ {
		switch document[index] {
		case '\\':
			index++
		case '"':
			return index + 1
		}
	}
	return len(document)
}

func literalEnd(document string, start int) int {
	index := start
	for index < len(document) && strings.IndexByte(",]} \t\n\r", document[index]) < 0 {
		index++
	}
	return index
}

func Join(rootHash string, lookup PartLookup) (string, error) {
	return Expand(Reference(rootHash), lookup)
}

func Expand(document string, lookup PartLookup) (string, error) {
	bodies, errorValue := fetchClosure(document, lookup)
	if errorValue != nil {
		return "", errorValue
	}
	joiner := partJoiner{bodies: bodies, joined: map[string]string{}}
	return joiner.expand(document), nil
}

func fetchClosure(document string, lookup PartLookup) (map[string]string, error) {
	bodies := map[string]string{}
	wanted := unfetchedReferences(document, bodies, nil)
	for len(wanted) > 0 {
		fetched, errorValue := lookup(wanted)
		if errorValue != nil {
			return nil, errorValue
		}
		next := []string{}
		for _, hash := range wanted {
			body, isStored := fetched[hash]
			if !isStored {
				return nil, fmt.Errorf("exchange part %s is not stored", hash)
			}
			bodies[hash] = body
			next = append(next, unfetchedReferences(body, bodies, next)...)
		}
		wanted = next
	}
	return bodies, nil
}

func unfetchedReferences(body string, fetched map[string]string, alreadyWanted []string) []string {
	references := []string{}
	for _, match := range partReferencePattern.FindAllStringSubmatch(body, -1) {
		hash := match[1]
		_, isFetched := fetched[hash]
		if isFetched || containsHash(alreadyWanted, hash) || containsHash(references, hash) {
			continue
		}
		references = append(references, hash)
	}
	return references
}

func containsHash(hashes []string, hash string) bool {
	for _, candidate := range hashes {
		if candidate == hash {
			return true
		}
	}
	return false
}

type partJoiner struct {
	bodies map[string]string
	joined map[string]string
}

func (joiner partJoiner) expand(document string) string {
	return partReferencePattern.ReplaceAllStringFunc(document, func(reference string) string {
		return joiner.join(partReferencePattern.FindStringSubmatch(reference)[1])
	})
}

func (joiner partJoiner) join(hash string) string {
	if joined, isJoined := joiner.joined[hash]; isJoined {
		return joined
	}
	joined := joiner.expand(joiner.bodies[hash])
	joiner.joined[hash] = joined
	return joined
}
