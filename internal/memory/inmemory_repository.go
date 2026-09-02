package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type InMemoryRepository struct {
	mutex      sync.Mutex
	episodes   map[string]Episode
	facts      map[string]Fact
	embeddings map[string][]float32
	profiles   map[string]Profile
	jobs       map[string]Job
	order      []string
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		episodes:   map[string]Episode{},
		facts:      map[string]Fact{},
		embeddings: map[string][]float32{},
		profiles:   map[string]Profile{},
		jobs:       map[string]Job{},
	}
}

func (repository *InMemoryRepository) HasVectorSearch(context.Context) (bool, error) {
	return true, nil
}

func (repository *InMemoryRepository) SaveEpisode(_ context.Context, write EpisodeWrite) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if errorValue := ValidateEpisode(write.Episode); errorValue != nil {
		return errorValue
	}
	for _, episode := range repository.episodes {
		if episode.SourceKind == write.Episode.SourceKind && episode.SourceID == write.Episode.SourceID {
			return fmt.Errorf("episode for %s %s already exists", episode.SourceKind, episode.SourceID)
		}
	}
	for _, factWrite := range write.Facts {
		if factWrite.ReinforcesFactID != "" {
			continue
		}
		if errorValue := ValidateFact(factWrite.Fact); errorValue != nil {
			return errorValue
		}
		if factWrite.SupersedesFactID != "" && !repository.isLiveLocked(factWrite.SupersedesFactID) {
			return fmt.Errorf("fact %s is not live and cannot be superseded", factWrite.SupersedesFactID)
		}
	}
	repository.episodes[write.Episode.EpisodeID] = write.Episode
	for _, factWrite := range write.Facts {
		if factWrite.ReinforcesFactID != "" {
			reinforced := repository.facts[factWrite.ReinforcesFactID]
			reinforced.ReinforcementCount++
			repository.facts[factWrite.ReinforcesFactID] = reinforced
			continue
		}
		fact := factWrite.Fact
		if fact.ReinforcementCount < 1 {
			fact.ReinforcementCount = 1
		}
		repository.facts[fact.FactID] = fact
		repository.order = append(repository.order, fact.FactID)
		if len(factWrite.Embedding) > 0 {
			repository.embeddings[fact.FactID] = append([]float32{}, factWrite.Embedding...)
		}
		if factWrite.SupersedesFactID != "" {
			superseded := repository.facts[factWrite.SupersedesFactID]
			superseded.SupersededBy = fact.FactID
			repository.facts[factWrite.SupersedesFactID] = superseded
		}
	}
	return nil
}

func (repository *InMemoryRepository) isLiveLocked(factID string) bool {
	fact, isFound := repository.facts[factID]
	return isFound && fact.SupersededBy == "" && fact.ForgottenAt.IsZero()
}

func (repository *InMemoryRepository) SearchFacts(_ context.Context, query FactSearchQuery) ([]RankedFact, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	readable := repository.readableFactsLocked(query.Reader, query.ReferenceTime)
	limit := query.CandidateLimit
	if limit <= 0 {
		limit = DefaultSearchCandidateLimit
	}
	ranked := map[string]*RankedFact{}
	for index, fact := range lexicalOrder(readable, query.Text, limit) {
		ranked[fact.FactID] = &RankedFact{Fact: fact, LexicalRank: index + 1}
	}
	if len(query.Embedding) > 0 {
		for index, fact := range repository.vectorOrderLocked(readable, query.Embedding, limit) {
			if hit, isRanked := ranked[fact.FactID]; isRanked {
				hit.VectorRank = index + 1
				continue
			}
			ranked[fact.FactID] = &RankedFact{Fact: fact, VectorRank: index + 1}
		}
	}
	hits := make([]RankedFact, 0, len(ranked))
	for _, hit := range ranked {
		hits = append(hits, *hit)
	}
	sort.Slice(hits, func(left int, right int) bool { return hits[left].Fact.FactID < hits[right].Fact.FactID })
	return hits, nil
}

func lexicalOrder(facts []Fact, text string, limit int) []Fact {
	terms := strings.Fields(strings.ToLower(text))
	type scored struct {
		fact  Fact
		score int
	}
	scoredFacts := []scored{}
	for _, fact := range facts {
		content := strings.ToLower(fact.Content)
		score := 0
		for _, term := range terms {
			if strings.Contains(content, term) {
				score++
			}
		}
		if score > 0 {
			scoredFacts = append(scoredFacts, scored{fact: fact, score: score})
		}
	}
	sort.SliceStable(scoredFacts, func(left int, right int) bool {
		if scoredFacts[left].score != scoredFacts[right].score {
			return scoredFacts[left].score > scoredFacts[right].score
		}
		return scoredFacts[left].fact.ValidFrom.After(scoredFacts[right].fact.ValidFrom)
	})
	ordered := []Fact{}
	for index, scoredFact := range scoredFacts {
		if index >= limit {
			break
		}
		ordered = append(ordered, scoredFact.fact)
	}
	return ordered
}

func (repository *InMemoryRepository) vectorOrderLocked(facts []Fact, queryEmbedding []float32, limit int) []Fact {
	type scored struct {
		fact       Fact
		similarity float64
	}
	scoredFacts := []scored{}
	for _, fact := range facts {
		embedding, hasEmbedding := repository.embeddings[fact.FactID]
		if !hasEmbedding {
			continue
		}
		scoredFacts = append(scoredFacts, scored{fact: fact, similarity: cosineSimilarity(queryEmbedding, embedding)})
	}
	sort.SliceStable(scoredFacts, func(left int, right int) bool {
		return scoredFacts[left].similarity > scoredFacts[right].similarity
	})
	ordered := []Fact{}
	for index, scoredFact := range scoredFacts {
		if index >= limit {
			break
		}
		ordered = append(ordered, scoredFact.fact)
	}
	return ordered
}

func cosineSimilarity(left []float32, right []float32) float64 {
	if len(left) != len(right) {
		return -1
	}
	var dot, leftNorm, rightNorm float64
	for index := range left {
		dot += float64(left[index]) * float64(right[index])
		leftNorm += float64(left[index]) * float64(left[index])
		rightNorm += float64(right[index]) * float64(right[index])
	}
	if leftNorm == 0 || rightNorm == 0 {
		return -1
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

func (repository *InMemoryRepository) readableFactsLocked(reader Reader, referenceTime time.Time) []Fact {
	readable := []Fact{}
	for _, factID := range repository.order {
		fact := repository.facts[factID]
		if fact.IsLive(referenceTime) && reader.CanRead(fact) {
			readable = append(readable, fact)
		}
	}
	return readable
}

func (repository *InMemoryRepository) ListFactsByID(_ context.Context, reader Reader, factIDs []string, referenceTime time.Time) ([]Fact, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	facts := []Fact{}
	for _, fact := range repository.readableFactsLocked(reader, referenceTime) {
		if containsString(factIDs, fact.FactID) {
			facts = append(facts, fact)
		}
	}
	return facts, nil
}

func (repository *InMemoryRepository) ListLiveFactsAboutPerson(_ context.Context, personID string, referenceTime time.Time) ([]Fact, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	facts := []Fact{}
	for _, factID := range repository.order {
		fact := repository.facts[factID]
		if fact.SubjectPersonID == personID && fact.IsLive(referenceTime) {
			facts = append(facts, fact)
		}
	}
	sort.SliceStable(facts, func(left int, right int) bool { return facts[left].ValidFrom.After(facts[right].ValidFrom) })
	return facts, nil
}

func (repository *InMemoryRepository) MarkFactsRecalled(_ context.Context, factIDs []string, recalledAt time.Time) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	for _, factID := range factIDs {
		if fact, isFound := repository.facts[factID]; isFound {
			fact.LastRecalledAt = recalledAt
			repository.facts[factID] = fact
		}
	}
	return nil
}

func (repository *InMemoryRepository) ForgetFacts(_ context.Context, reader Reader, factIDs []string, reason string, forgottenAt time.Time) ([]string, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	forgotten := []string{}
	for _, fact := range repository.readableFactsLocked(reader, forgottenAt) {
		if !containsString(factIDs, fact.FactID) {
			continue
		}
		fact.ForgottenAt = forgottenAt
		fact.ForgetReason = reason
		repository.facts[fact.FactID] = fact
		forgotten = append(forgotten, fact.FactID)
	}
	return forgotten, nil
}

func (repository *InMemoryRepository) FindFact(factID string) (Fact, bool) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	fact, isFound := repository.facts[factID]
	return fact, isFound
}

func (repository *InMemoryRepository) AllFacts() []Fact {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	facts := make([]Fact, 0, len(repository.order))
	for _, factID := range repository.order {
		facts = append(facts, repository.facts[factID])
	}
	return facts
}

func (repository *InMemoryRepository) FindProfile(_ context.Context, personID string) (Profile, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	profile, isFound := repository.profiles[personID]
	return profile, isFound, nil
}

func (repository *InMemoryRepository) SaveProfile(_ context.Context, profile Profile) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if strings.TrimSpace(profile.PersonID) == "" {
		return errors.New("profile person id is required")
	}
	repository.profiles[profile.PersonID] = profile
	return nil
}

func (repository *InMemoryRepository) EnqueueJob(_ context.Context, kind string, subjectID string, runAfter time.Time) (Job, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	for _, job := range repository.jobs {
		if job.Kind == kind && job.SubjectID == subjectID && job.FinishedAt.IsZero() {
			return job, false, nil
		}
	}
	job := Job{JobID: taskstate.NewIdentifier(), Kind: kind, SubjectID: subjectID, RunAfter: runAfter, CreatedAt: runAfter}
	repository.jobs[job.JobID] = job
	return job, true, nil
}

func (repository *InMemoryRepository) ClaimDueJobs(_ context.Context, kinds []string, referenceTime time.Time, leaseDuration time.Duration, limit int) ([]Job, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	due := []Job{}
	for _, job := range repository.jobs {
		isLeased := !job.LockedUntil.IsZero() && job.LockedUntil.After(referenceTime)
		if job.FinishedAt.IsZero() && containsString(kinds, job.Kind) && !job.RunAfter.After(referenceTime) && !isLeased {
			due = append(due, job)
		}
	}
	sort.Slice(due, func(left int, right int) bool { return due[left].RunAfter.Before(due[right].RunAfter) })
	claimed := []Job{}
	for index, job := range due {
		if index >= limit {
			break
		}
		job.Attempts++
		job.LockedUntil = referenceTime.Add(leaseDuration)
		repository.jobs[job.JobID] = job
		claimed = append(claimed, job)
	}
	return claimed, nil
}

func (repository *InMemoryRepository) FinishJob(_ context.Context, jobID string, finishedAt time.Time) error {
	return repository.updateJob(jobID, func(job *Job) { job.FinishedAt = finishedAt; job.LockedUntil = time.Time{}; job.LastError = "" })
}

func (repository *InMemoryRepository) RetryJob(_ context.Context, jobID string, lastError string, runAfter time.Time) error {
	return repository.updateJob(jobID, func(job *Job) { job.RunAfter = runAfter; job.LockedUntil = time.Time{}; job.LastError = lastError })
}

func (repository *InMemoryRepository) AbandonJob(_ context.Context, jobID string, lastError string, finishedAt time.Time) error {
	return repository.updateJob(jobID, func(job *Job) { job.FinishedAt = finishedAt; job.LockedUntil = time.Time{}; job.LastError = lastError })
}

func (repository *InMemoryRepository) FindJob(jobID string) (Job, bool) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	job, isFound := repository.jobs[jobID]
	return job, isFound
}

func (repository *InMemoryRepository) updateJob(jobID string, mutate func(*Job)) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	job, isFound := repository.jobs[jobID]
	if !isFound {
		return fmt.Errorf("memory job %s not found", jobID)
	}
	mutate(&job)
	repository.jobs[jobID] = job
	return nil
}
