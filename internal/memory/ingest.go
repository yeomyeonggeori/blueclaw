package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const (
	FactRelationNew        = "new"
	FactRelationSupersedes = "supersedes"
	FactRelationReinforces = "reinforces"
)

const (
	DefaultIngestCandidateLimit = 24
	ingestMaximumFactCount      = 12
)

const ingestSchemaDocument = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["facts"],
  "properties": {
    "facts": {
      "type": "array",
      "maxItems": 12,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["content", "kind", "scope", "subjectPersonHint", "relation", "relatedFactID", "validUntil"],
        "properties": {
          "content": {"type": "string", "maxLength": 240},
          "kind": {"type": "string", "enum": ["identity", "preference", "fact", "episode", "temporary"]},
          "scope": {"type": "string", "enum": ["private", "circle", "workspace"]},
          "subjectPersonHint": {"type": "string"},
          "relation": {"type": "string", "enum": ["new", "supersedes", "reinforces"]},
          "relatedFactID": {"type": "string"},
          "validUntil": {"type": "string"}
        }
      }
    }
  }
}`

const ingestInstruction = `You maintain an assistant's long-term memory about the people it works with. You read one source (a finished task's transcript, or one sentence a person asked the assistant to remember) together with the memory facts that already exist and are closest to it, and you decide what the memory should hold afterwards.

Each fact is one atomic sentence about one subject, written in the language the source uses, naming the person rather than saying "the user". Keep only what will still matter after this task: who someone is, what they prefer, what is true about their work, what happened that is worth recalling, and time-bound states with an end date. Do not turn the task request itself, tool output, or small talk into facts.

kind:
- "identity": stable facts about who a person is (name to use, role, team).
- "preference": how a person wants things done.
- "fact": something true until it changes.
- "episode": something that happened, with its date if the source gives one.
- "temporary": a state with an end date; put that date in validUntil as YYYY-MM-DD. Every other kind leaves validUntil as "".

scope:
- "private": about the requester, for the requester alone.
- "circle": shared with the active circle named in the context; use it only when the context names one.
- "workspace": true for everyone in the company.

relation, judged against the existing facts you were given:
- "supersedes" with relatedFactID when the new content replaces an existing fact that is no longer true.
- "reinforces" with relatedFactID when the source repeats an existing preference or fact without changing it; then content may be a copy.
- "new" with relatedFactID "" otherwise.
relatedFactID must be one of the IDs listed under existing facts; never invent one.

subjectPersonHint is the exact name of the person the fact is about as it appears in the source or the context, or "" when it is about nobody in particular.

Return an empty facts list when the source holds nothing worth remembering. Do not add requirements the source does not state, do not infer what it does not say, and do not repeat an existing fact as "new".`

type PersonResolver interface {
	ResolvePersonIDByDisplayName(displayName string) (string, bool)
}

type IngestRequest struct {
	Episode        Episode
	Reader         Reader
	RequesterName  string
	ActiveCircleID string
	Label          SecurityLabel
	KnownPeople    map[string]string
}

type IngestResult struct {
	EpisodeID         string   `json:"episodeID"`
	Facts             []Fact   `json:"facts"`
	SupersededFactIDs []string `json:"supersededFactIDs"`
	ReinforcedFactIDs []string `json:"reinforcedFactIDs"`
	CandidateCount    int      `json:"candidateCount"`
}

type Ingester struct {
	Store          Store
	Model          model.LanguageModelProvider
	People         PersonResolver
	CandidateLimit int
	Now            func() time.Time
}

type ingestOutput struct {
	Facts []ingestOutputFact `json:"facts"`
}

type ingestOutputFact struct {
	Content           string `json:"content"`
	Kind              string `json:"kind"`
	Scope             string `json:"scope"`
	SubjectPersonHint string `json:"subjectPersonHint"`
	Relation          string `json:"relation"`
	RelatedFactID     string `json:"relatedFactID"`
	ValidUntil        string `json:"validUntil"`
}

func (ingester Ingester) Ingest(ctx context.Context, request IngestRequest) (IngestResult, error) {
	if ingester.Model == nil {
		return IngestResult{}, errors.New("memory ingestion has no language model")
	}
	if ingester.Store.Facts == nil || ingester.Store.Embedder == nil {
		return IngestResult{}, errors.New("memory ingestion has no fact repository or embedder")
	}
	if errorValue := ValidateEpisode(request.Episode); errorValue != nil {
		return IngestResult{}, TerminalJobError{Cause: errorValue}
	}
	now := ingester.now()
	candidates, errorValue := ingester.candidates(ctx, request, now)
	if errorValue != nil {
		return IngestResult{}, errorValue
	}
	output, errorValue := ingester.askModel(ctx, request, candidates, now)
	if errorValue != nil {
		return IngestResult{}, errorValue
	}
	writes, errorValue := ingester.factWrites(request, candidates, output, now)
	if errorValue != nil {
		return IngestResult{}, TerminalJobError{Cause: errorValue}
	}
	if errorValue := ingester.embedNewFacts(ctx, writes); errorValue != nil {
		return IngestResult{}, errorValue
	}
	if errorValue := ingester.Store.Facts.SaveEpisode(ctx, EpisodeWrite{Episode: request.Episode, Facts: writes}); errorValue != nil {
		return IngestResult{}, errorValue
	}
	result := summarizeIngest(request.Episode.EpisodeID, writes, len(candidates))
	ingester.enqueueProfileRebuilds(ctx, writes, candidates)
	return result, nil
}

func (ingester Ingester) candidates(ctx context.Context, request IngestRequest, now time.Time) ([]Fact, error) {
	embedding, errorValue := ingester.Store.Embedder.EmbedQuery(ctx, request.Episode.Content)
	if errorValue != nil {
		return nil, fmt.Errorf("episode embedding failed: %w", errorValue)
	}
	if errorValue := ValidateEmbedding(embedding); errorValue != nil {
		return nil, errorValue
	}
	hits, errorValue := ingester.Store.Facts.SearchFacts(ctx, FactSearchQuery{
		Reader:         request.Reader,
		Text:           request.Episode.Content,
		Embedding:      embedding,
		CandidateLimit: ingester.candidateLimit(),
		ReferenceTime:  now,
	})
	if errorValue != nil {
		return nil, errorValue
	}
	ranked := RankFacts(hits, now)
	candidates := make([]Fact, 0, len(ranked))
	for index, scoredFact := range ranked {
		if index >= ingester.candidateLimit() {
			break
		}
		candidates = append(candidates, scoredFact.Fact)
	}
	return candidates, nil
}

func (ingester Ingester) askModel(ctx context.Context, request IngestRequest, candidates []Fact, now time.Time) (ingestOutput, error) {
	response, errorValue := ingester.Model.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: ingestInstruction},
			{Role: "user", Content: ingestSubject(request, candidates, now)},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               "memory_ingest",
			Document:           ingestSchemaDocument,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return ingestOutput{}, fmt.Errorf("memory ingestion model call failed: %w", errorValue)
	}
	var output ingestOutput
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(response.Content)), &output); errorValue != nil {
		return ingestOutput{}, TerminalJobError{Cause: fmt.Errorf("memory ingestion output is not the schema: %w", errorValue)}
	}
	return output, nil
}

func ingestSubject(request IngestRequest, candidates []Fact, now time.Time) string {
	lines := []string{
		"Today: " + now.Format("2006-01-02"),
		"Requester: " + firstNonEmptyTrimmed(request.RequesterName, request.Episode.RequesterPersonID),
	}
	if strings.TrimSpace(request.ActiveCircleID) != "" {
		lines = append(lines, "Active circle: "+request.ActiveCircleID)
	} else {
		lines = append(lines, "Active circle: none")
	}
	lines = append(lines, "", "Existing facts closest to the source:")
	if len(candidates) == 0 {
		lines = append(lines, "(none)")
	}
	for _, candidate := range candidates {
		lines = append(lines, fmt.Sprintf("- id=%s kind=%s scope=%s validFrom=%s: %s", candidate.FactID, candidate.Kind, candidate.ScopeType, candidate.ValidFrom.Format("2006-01-02"), candidate.Content))
	}
	lines = append(lines, "", "Source ("+request.Episode.SourceKind+", "+request.Episode.OccurredAt.Format("2006-01-02")+"):", request.Episode.Content)
	return strings.Join(lines, "\n")
}

func (ingester Ingester) factWrites(request IngestRequest, candidates []Fact, output ingestOutput, now time.Time) ([]FactWrite, error) {
	if len(output.Facts) > ingestMaximumFactCount {
		return nil, fmt.Errorf("memory ingestion returned %d facts, the ceiling is %d", len(output.Facts), ingestMaximumFactCount)
	}
	candidateByID := map[string]Fact{}
	for _, candidate := range candidates {
		candidateByID[candidate.FactID] = candidate
	}
	writes := make([]FactWrite, 0, len(output.Facts))
	relatedFactIDs := map[string]bool{}
	for index, outputFact := range output.Facts {
		write, errorValue := ingester.factWrite(request, candidateByID, outputFact, now)
		if errorValue != nil {
			return nil, fmt.Errorf("fact %d: %w", index+1, errorValue)
		}
		relatedFactID := firstNonEmptyTrimmed(write.SupersedesFactID, write.ReinforcesFactID)
		if relatedFactID != "" {
			if relatedFactIDs[relatedFactID] {
				return nil, fmt.Errorf("fact %d relates to %s, which an earlier fact already relates to", index+1, relatedFactID)
			}
			relatedFactIDs[relatedFactID] = true
		}
		writes = append(writes, write)
	}
	return writes, nil
}

func (ingester Ingester) factWrite(request IngestRequest, candidateByID map[string]Fact, outputFact ingestOutputFact, now time.Time) (FactWrite, error) {
	relatedFactID := strings.TrimSpace(outputFact.RelatedFactID)
	switch outputFact.Relation {
	case FactRelationReinforces:
		if _, isCandidate := candidateByID[relatedFactID]; !isCandidate {
			return FactWrite{}, fmt.Errorf("reinforces %q, which is not among the existing facts", relatedFactID)
		}
		return FactWrite{ReinforcesFactID: relatedFactID}, nil
	case FactRelationSupersedes:
		if _, isCandidate := candidateByID[relatedFactID]; !isCandidate {
			return FactWrite{}, fmt.Errorf("supersedes %q, which is not among the existing facts", relatedFactID)
		}
	case FactRelationNew:
		if relatedFactID != "" {
			return FactWrite{}, fmt.Errorf("relation new carries relatedFactID %q", relatedFactID)
		}
	default:
		return FactWrite{}, fmt.Errorf("relation %q is not new, supersedes, or reinforces", outputFact.Relation)
	}
	fact, errorValue := ingester.newFact(request, outputFact, now)
	if errorValue != nil {
		return FactWrite{}, errorValue
	}
	write := FactWrite{Fact: fact}
	if outputFact.Relation == FactRelationSupersedes {
		write.SupersedesFactID = relatedFactID
	}
	return write, nil
}

func (ingester Ingester) newFact(request IngestRequest, outputFact ingestOutputFact, now time.Time) (Fact, error) {
	fact := Fact{
		FactID:            taskstate.NewIdentifier(),
		EpisodeID:         request.Episode.EpisodeID,
		ScopeType:         outputFact.Scope,
		Kind:              outputFact.Kind,
		Content:           strings.Join(strings.Fields(outputFact.Content), " "),
		EmbeddingModel:    ingester.Store.EmbeddingModel,
		SecurityLevelRank: request.Label.SecurityLevelRank,
		RequiredClasses:   nonNilStringSlice(request.Label.RequiredClasses),
		ValidFrom:         request.Episode.OccurredAt.UTC(),
		SubjectPersonID:   ingester.resolveSubject(request, outputFact.SubjectPersonHint),
	}
	switch fact.ScopeType {
	case ScopeTypeCircle:
		if strings.TrimSpace(request.ActiveCircleID) == "" || !containsString(request.Reader.CircleIDs, request.ActiveCircleID) {
			fact.ScopeType = ScopeTypePrivate
			fact.ScopeID = request.Episode.RequesterPersonID
		} else {
			fact.ScopeID = request.ActiveCircleID
		}
	case ScopeTypePrivate:
		fact.ScopeID = request.Episode.RequesterPersonID
	}
	if fact.ScopeType == ScopeTypePrivate {
		fact.SecurityLevelRank = 0
		fact.RequiredClasses = []string{}
		if fact.SubjectPersonID == "" {
			fact.SubjectPersonID = request.Episode.RequesterPersonID
		}
	}
	if fact.Kind == FactKindTemporary {
		validUntil, errorValue := parseValidUntil(outputFact.ValidUntil, now)
		if errorValue != nil {
			return Fact{}, errorValue
		}
		fact.ValidUntil = validUntil
	} else if strings.TrimSpace(outputFact.ValidUntil) != "" {
		return Fact{}, fmt.Errorf("a %s fact carries validUntil %q", fact.Kind, outputFact.ValidUntil)
	}
	if errorValue := ValidateFact(fact); errorValue != nil {
		return Fact{}, errorValue
	}
	return fact, nil
}

func parseValidUntil(value string, now time.Time) (time.Time, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return time.Time{}, errors.New("a temporary fact requires validUntil")
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if parsed, errorValue := time.Parse(layout, trimmedValue); errorValue == nil {
			if layout == "2006-01-02" {
				parsed = parsed.Add(24 * time.Hour)
			}
			if !parsed.After(now) {
				return time.Time{}, fmt.Errorf("validUntil %s is already in the past", trimmedValue)
			}
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("validUntil %q is not a YYYY-MM-DD date", trimmedValue)
}

func (ingester Ingester) resolveSubject(request IngestRequest, hint string) string {
	trimmedHint := strings.TrimSpace(hint)
	if trimmedHint == "" {
		return ""
	}
	if personID, isKnown := request.KnownPeople[trimmedHint]; isKnown {
		return personID
	}
	if strings.EqualFold(trimmedHint, strings.TrimSpace(request.RequesterName)) {
		return request.Episode.RequesterPersonID
	}
	if ingester.People != nil {
		if personID, isFound := ingester.People.ResolvePersonIDByDisplayName(trimmedHint); isFound {
			return personID
		}
	}
	return ""
}

func (ingester Ingester) embedNewFacts(ctx context.Context, writes []FactWrite) error {
	contents := []string{}
	indexes := []int{}
	for index, write := range writes {
		if write.ReinforcesFactID != "" {
			continue
		}
		contents = append(contents, write.Fact.Content)
		indexes = append(indexes, index)
	}
	if len(contents) == 0 {
		return nil
	}
	embeddings, errorValue := ingester.Store.Embedder.EmbedDocuments(ctx, contents)
	if errorValue != nil {
		return fmt.Errorf("fact embedding failed: %w", errorValue)
	}
	if len(embeddings) != len(contents) {
		return fmt.Errorf("fact embedding returned %d vectors for %d facts", len(embeddings), len(contents))
	}
	for position, index := range indexes {
		if errorValue := ValidateEmbedding(embeddings[position]); errorValue != nil {
			return errorValue
		}
		writes[index].Embedding = embeddings[position]
	}
	return nil
}

func summarizeIngest(episodeID string, writes []FactWrite, candidateCount int) IngestResult {
	result := IngestResult{EpisodeID: episodeID, Facts: []Fact{}, SupersededFactIDs: []string{}, ReinforcedFactIDs: []string{}, CandidateCount: candidateCount}
	for _, write := range writes {
		if write.ReinforcesFactID != "" {
			result.ReinforcedFactIDs = append(result.ReinforcedFactIDs, write.ReinforcesFactID)
			continue
		}
		result.Facts = append(result.Facts, write.Fact)
		if write.SupersedesFactID != "" {
			result.SupersededFactIDs = append(result.SupersededFactIDs, write.SupersedesFactID)
		}
	}
	return result
}

func (ingester Ingester) enqueueProfileRebuilds(ctx context.Context, writes []FactWrite, candidates []Fact) {
	candidateByID := map[string]Fact{}
	for _, candidate := range candidates {
		candidateByID[candidate.FactID] = candidate
	}
	touched := map[string]bool{}
	for _, write := range writes {
		touched[write.Fact.SubjectPersonID] = true
		if related, isCandidate := candidateByID[firstNonEmptyTrimmed(write.SupersedesFactID, write.ReinforcesFactID)]; isCandidate {
			touched[related.SubjectPersonID] = true
		}
	}
	for personID := range touched {
		ingester.Store.enqueueProfileRebuild(ctx, personID)
	}
}

func (ingester Ingester) candidateLimit() int {
	if ingester.CandidateLimit > 0 {
		return ingester.CandidateLimit
	}
	return DefaultIngestCandidateLimit
}

func (ingester Ingester) now() time.Time {
	if ingester.Now != nil {
		return ingester.Now().UTC()
	}
	return time.Now().UTC()
}
