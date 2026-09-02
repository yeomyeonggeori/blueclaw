package memory

import (
	"context"
	"time"
)

type FactSearchQuery struct {
	Reader         Reader
	Text           string
	Embedding      []float32
	CandidateLimit int
	ReferenceTime  time.Time
}

type RankedFact struct {
	Fact        Fact
	VectorRank  int
	LexicalRank int
}

type FactRepository interface {
	HasVectorSearch(ctx context.Context) (bool, error)
	SaveEpisode(ctx context.Context, write EpisodeWrite) error
	SearchFacts(ctx context.Context, query FactSearchQuery) ([]RankedFact, error)
	ListFactsByID(ctx context.Context, reader Reader, factIDs []string, referenceTime time.Time) ([]Fact, error)
	ListLiveFactsAboutPerson(ctx context.Context, personID string, referenceTime time.Time) ([]Fact, error)
	MarkFactsRecalled(ctx context.Context, factIDs []string, recalledAt time.Time) error
	ForgetFacts(ctx context.Context, reader Reader, factIDs []string, reason string, forgottenAt time.Time) ([]string, error)
}

type ProfileRepository interface {
	FindProfile(ctx context.Context, personID string) (Profile, bool, error)
	SaveProfile(ctx context.Context, profile Profile) error
}

type JobRepository interface {
	EnqueueJob(ctx context.Context, kind string, subjectID string, runAfter time.Time) (Job, bool, error)
	ClaimDueJobs(ctx context.Context, kinds []string, referenceTime time.Time, leaseDuration time.Duration, limit int) ([]Job, error)
	FinishJob(ctx context.Context, jobID string, finishedAt time.Time) error
	RetryJob(ctx context.Context, jobID string, lastError string, runAfter time.Time) error
	AbandonJob(ctx context.Context, jobID string, lastError string, finishedAt time.Time) error
}
