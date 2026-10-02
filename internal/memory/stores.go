package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluememo"
)

const (
	ScopePerson    = "person"
	ScopeCircle    = "circle"
	ScopeWorkspace = "workspace"
)

// memoryFileMode lets the subject's group read a memory and nobody but the
// service write it: a subject's memory is kept on their behalf.
const (
	memoryFileName         = "memory.db"
	memoryFileMode         = 0o640
	readTimeoutSecond      = 20
	readOutputMaximumBytes = 4 << 20
)

// Scope names one memory file. A subject's memory is a file of their own, so
// what a reader may read is which of those files their POSIX identity opens,
// rather than a label carried by every fact.
type Scope struct {
	Kind string
	ID   string
}

func PersonScope(personID string) Scope { return Scope{Kind: ScopePerson, ID: personID} }
func CircleScope(circleID string) Scope { return Scope{Kind: ScopeCircle, ID: circleID} }
func WorkspaceScope() Scope             { return Scope{Kind: ScopeWorkspace} }

// pathUnder puts a subject's memory in that subject's own workspace
// directory, where the permissions the workspace already declares are what
// decide who opens it.
func (scope Scope) pathUnder(workspaceRootPath string) (string, error) {
	if strings.TrimSpace(workspaceRootPath) == "" {
		return "", errors.New("memory has no workspace root to sit under")
	}
	directory := scope.directoryUnder(workspaceRootPath)
	if directory == "" {
		return "", fmt.Errorf("memory scope %q with identifier %q names no directory", scope.Kind, scope.ID)
	}
	return filepath.Join(security.ProtectedDirectoryPath(directory), memoryFileName), nil
}

func (scope Scope) directoryUnder(workspaceRootPath string) string {
	switch scope.Kind {
	case ScopePerson:
		return security.PersonHomeDirectoryPath(workspaceRootPath, scope.ID)
	case ScopeCircle:
		return security.CircleDirectoryPath(workspaceRootPath, scope.ID)
	case ScopeWorkspace:
		return security.SharedDirectoryPath(workspaceRootPath)
	}
	return ""
}

// Stores holds the memory files this company's agent reads and writes, opening
// each on first use and keeping it for the life of the process.
type Stores struct {
	actor             security.WorkspaceActorFactory
	workspaceRootPath string
	configuration     bluememo.Configuration
	mutex             sync.Mutex
	open              map[string]*bluememo.Store
}

func NewStores(workspaceRootPath string, configuration bluememo.Configuration, actor security.WorkspaceActorFactory) *Stores {
	return &Stores{actor: actor,
		workspaceRootPath: workspaceRootPath, configuration: configuration, open: map[string]*bluememo.Store{}}
}

func (stores *Stores) Store(ctx context.Context, scope Scope) (*bluememo.Store, error) {
	path, errorValue := stores.Path(scope)
	if errorValue != nil {
		return nil, errorValue
	}
	stores.mutex.Lock()
	defer stores.mutex.Unlock()
	if held, isOpen := stores.open[path]; isOpen {
		return held, nil
	}
	if errorValue := makeHoldingDirectory(path); errorValue != nil {
		return nil, errorValue
	}
	opened, errorValue := bluememo.Open(ctx, path, stores.configuration)
	if errorValue != nil {
		return nil, errorValue
	}
	if errorValue := os.Chmod(path, memoryFileMode); errorValue != nil {
		_ = opened.Close()
		return nil, fmt.Errorf("let the subject read %s: %w", path, errorValue)
	}
	stores.open[path] = opened
	return opened, nil
}

func (stores *Stores) Close() error {
	stores.mutex.Lock()
	defer stores.mutex.Unlock()
	var firstFailure error
	for fileName, held := range stores.open {
		if errorValue := held.Close(); errorValue != nil && firstFailure == nil {
			firstFailure = errorValue
		}
		delete(stores.open, fileName)
	}
	return firstFailure
}

// ScopesForAccess is every memory file a reader may be shown, in the order a
// recall merges them.
// ScopesForAccess says where to look for a reader's memory. It grants nothing:
// a recall runs as that reader, so a file this list names and their identity
// cannot open is refused by the kernel. A list too generous costs a refusal,
// and a list too narrow costs a memory nobody finds.
func ScopesForAccess(personAccess policy.PersonAccess, containedCircles map[string][]string) []Scope {
	scopes := []Scope{}
	if personAccess.PersonID != "" {
		scopes = append(scopes, PersonScope(personAccess.PersonID))
	}
	seen := map[string]bool{}
	for _, circleID := range personAccess.Circles {
		for _, reachable := range append([]string{circleID}, containedCircles[circleID]...) {
			if reachable == "" || seen[reachable] {
				continue
			}
			seen[reachable] = true
			scopes = append(scopes, CircleScope(reachable))
		}
	}
	return append(scopes, WorkspaceScope())
}

// RecallAcross asks every file the reader may be shown and returns what the
// agent loop reads, most relevant first. A scope with no file yet has nothing
// to say, which is not a failure.
// RecallAcross reads each file as the person the recall is for, so a file
// their identity cannot open is refused by the kernel rather than by a list
// this code keeps. The scopes say where to look; they decide nothing.
func (stores *Stores) RecallAcross(ctx context.Context, personAccess policy.PersonAccess, scopes []Scope, query string, limit int) (Recalled, error) {
	recalled := Recalled{Mode: "merged"}
	if len(scopes) == 0 {
		return recalled, nil
	}
	read, errorValue := stores.readerFor(ctx, personAccess, query)
	if errorValue != nil {
		return recalled, errorValue
	}
	for _, scope := range scopes {
		result, errorValue := read(ctx, scope, limit)
		if errorValue != nil {
			return recalled, errorValue
		}
		if result.DegradedReason != "" && recalled.DegradedReason == "" {
			recalled.DegradedReason = result.DegradedReason
		}
		for _, entry := range result.Memories {
			recalled.Facts = append(recalled.Facts, memoryFactFrom(entry, scope))
		}
	}
	sort.SliceStable(recalled.Facts, func(first, second int) bool {
		return recalled.Facts[first].Score > recalled.Facts[second].Score
	})
	if len(recalled.Facts) > limit {
		recalled.Facts = recalled.Facts[:limit]
	}
	return recalled, nil
}

// Recalled is what one agent turn reads out of memory.
type Recalled struct {
	Facts          []MemoryFact
	Mode           string
	DegradedReason string
}

func memoryFactFrom(entry bluememo.RecalledMemory, scope Scope) MemoryFact {
	sourceKind := "fact"
	if entry.Memory.IsStatic {
		sourceKind = "identity"
	}
	validAt := entry.Memory.OccurredAt
	if validAt.IsZero() {
		validAt = entry.Memory.CreatedAt
	}
	return MemoryFact{
		FactID:          entry.Memory.MemoryID,
		ScopeType:       scope.Kind,
		Content:         entry.Memory.Content,
		Score:           entry.Score,
		SourceEpisodeID: entry.Memory.OriginID,
		SourceKind:      sourceKind,
		ValidAt:         validAt,
	}
}

// Remember takes what was said into one scope's file and settles it, so the
// caller's next recall can see it.
func (stores *Stores) Remember(ctx context.Context, scope Scope, note bluememo.Note) (bluememo.SettleReport, error) {
	store, errorValue := stores.Store(ctx, scope)
	if errorValue != nil {
		return bluememo.SettleReport{}, errorValue
	}
	if errorValue := store.Memorize(ctx, note); errorValue != nil {
		return bluememo.SettleReport{}, errorValue
	}
	return store.Settle(ctx)
}

const DefaultRecallLimit = 12

// IdentityFactCount is how much of a recall is what the person is rather than
// what happened, which the loop reports so a turn's memory can be read.
func IdentityFactCount(facts []MemoryFact) int {
	count := 0
	for _, fact := range facts {
		if fact.SourceKind == "identity" {
			count++
		}
	}
	return count
}

// ForgetAcross forgets what the reader asked to forget, wherever among the
// files they may be shown it is held.
func (stores *Stores) ForgetAcross(ctx context.Context, scopes []Scope, memoryIDs []string, requestPhrase string) ([]string, error) {
	forgotten := []string{}
	for _, scope := range scopes {
		store, errorValue := stores.Store(ctx, scope)
		if errorValue != nil {
			return nil, errorValue
		}
		memories, errorValue := store.ForgetMemories(ctx, memoryIDs, requestPhrase)
		if errorValue != nil {
			return nil, errorValue
		}
		for _, forgottenMemory := range memories {
			forgotten = append(forgotten, forgottenMemory.MemoryID)
		}
	}
	return forgotten, nil
}

// NewIdentifier names one note's origin bundle, so what was said in one breath
// settles together.
func NewIdentifier() string {
	identifierBytes := make([]byte, 16)
	if _, errorValue := rand.Read(identifierBytes); errorValue != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(identifierBytes)
}

const reembedBatchSize = 64

// Maintain sweeps what has gone cold and re-embeds what a changed embedding
// model left stale, across every memory file on disk rather than only the ones
// this process has already opened.
func (stores *Stores) Maintain(ctx context.Context) error {
	scopes, errorValue := stores.storedScopes()
	if errorValue != nil {
		return errorValue
	}
	for _, scope := range scopes {
		store, errorValue := stores.Store(ctx, scope)
		if errorValue != nil {
			return errorValue
		}
		if _, errorValue := store.Sweep(ctx); errorValue != nil {
			return errorValue
		}
		if _, errorValue := store.Reembed(ctx, reembedBatchSize); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func (stores *Stores) storedScopes() ([]Scope, error) {
	scopes := []Scope{}
	if _, errorValue := os.Stat(filepath.Join(stores.workspaceRootPath, "workspace.db")); errorValue == nil {
		scopes = append(scopes, WorkspaceScope())
	}
	for _, kind := range []string{ScopePerson, ScopeCircle} {
		entries, errorValue := os.ReadDir(filepath.Join(stores.workspaceRootPath, kind+"s"))
		if errorValue != nil {
			if os.IsNotExist(errorValue) {
				continue
			}
			return nil, errorValue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".db" {
				continue
			}
			scopes = append(scopes, Scope{Kind: kind, ID: strings.TrimSuffix(entry.Name(), ".db")})
		}
	}
	return scopes, nil
}

// MergePerson moves one person's memory into another's, for when two records
// turn out to be the same person. The losing file is renamed when the survivor
// has none, and its memories are adopted when both exist.
//
// Adopted memories arrive without their vectors, because a file does not hand
// them back, so the maintenance pass reembeds them and until then they answer
// on their wording.
func (stores *Stores) MergePerson(ctx context.Context, fromPersonID string, toPersonID string) error {
	if fromPersonID == "" || toPersonID == "" || fromPersonID == toPersonID {
		return nil
	}
	fromPath, errorValue := stores.Path(PersonScope(fromPersonID))
	if errorValue != nil {
		return errorValue
	}
	if _, errorValue := os.Stat(fromPath); errors.Is(errorValue, os.ErrNotExist) {
		return nil
	}
	toPath, errorValue := stores.Path(PersonScope(toPersonID))
	if errorValue != nil {
		return errorValue
	}
	stores.forget(PersonScope(fromPersonID))
	if _, errorValue := os.Stat(toPath); errors.Is(errorValue, os.ErrNotExist) {
		stores.forget(PersonScope(toPersonID))
		if errorValue := makeHoldingDirectory(toPath); errorValue != nil {
			return errorValue
		}
		return os.Rename(fromPath, toPath)
	}
	return stores.adoptEveryMemory(ctx, fromPersonID, toPersonID, fromPath)
}

func (stores *Stores) adoptEveryMemory(ctx context.Context, fromPersonID string, toPersonID string, fromPath string) error {
	losing, errorValue := stores.Store(ctx, PersonScope(fromPersonID))
	if errorValue != nil {
		return errorValue
	}
	memories, errorValue := losing.Memories(ctx)
	if errorValue != nil {
		return errorValue
	}
	surviving, errorValue := stores.Store(ctx, PersonScope(toPersonID))
	if errorValue != nil {
		return errorValue
	}
	adopted := make([]bluememo.AdoptedMemory, 0, len(memories))
	for _, memory := range memories {
		adopted = append(adopted, bluememo.AdoptedMemory{Memory: memory})
	}
	if _, errorValue := surviving.Adopt(ctx, adopted); errorValue != nil {
		return errorValue
	}
	stores.forget(PersonScope(fromPersonID))
	return os.Remove(fromPath)
}

// readerFor embeds the query once, because the service holds the credentials
// for that, and answers with a way to read one file under the reader's own
// POSIX identity.
func (stores *Stores) readerFor(ctx context.Context, personAccess policy.PersonAccess, query string) (func(context.Context, Scope, int) (bluememo.RecallResult, error), error) {
	vector, errorValue := stores.configuration.Embedder.EmbedQuery(ctx, query)
	if errorValue != nil {
		return nil, fmt.Errorf("embed the recall query: %w", errorValue)
	}
	actor, errorValue := stores.actor.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      personAccess,
		WorkspaceRootPath: stores.workspaceRootPath,
	})
	if errorValue != nil {
		return nil, fmt.Errorf("read memory as %s: %w", personAccess.PersonID, errorValue)
	}
	executablePath, errorValue := os.Executable()
	if errorValue != nil {
		return nil, fmt.Errorf("find the binary that serves a read: %w", errorValue)
	}
	return func(ctx context.Context, scope Scope, limit int) (bluememo.RecallResult, error) {
		path, errorValue := stores.Path(scope)
		if errorValue != nil {
			return bluememo.RecallResult{}, errorValue
		}
		request, errorValue := json.Marshal(ReadRequest{
			StorePath:      path,
			Query:          query,
			QueryVector:    vector,
			EmbeddingModel: stores.configuration.EmbeddingModel,
			Limit:          limit,
			LaneDepth:      stores.configuration.LaneDepth,
			RecallSources:  stores.configuration.RecallSources,
		})
		if errorValue != nil {
			return bluememo.RecallResult{}, errorValue
		}
		return runRead(ctx, actor, executablePath, request)
	}, nil
}

func runRead(ctx context.Context, actor security.WorkspaceActor, executablePath string, request []byte) (bluememo.RecallResult, error) {
	result, errorValue := actor.Run(ctx, security.CommandRequest{
		ExecutableName:     executablePath,
		Arguments:          []string{ReadCommand},
		Stdin:              string(request),
		TimeoutSecond:      readTimeoutSecond,
		OutputMaximumBytes: readOutputMaximumBytes,
	})
	if errorValue != nil {
		return bluememo.RecallResult{}, errorValue
	}
	if result.ExitCode != 0 {
		return bluememo.RecallResult{}, fmt.Errorf("a read refused: %s", strings.TrimSpace(result.Stderr))
	}
	var recalled bluememo.RecallResult
	if errorValue := json.Unmarshal([]byte(result.Stdout), &recalled); errorValue != nil {
		return bluememo.RecallResult{}, fmt.Errorf("decode what a read answered: %w", errorValue)
	}
	return recalled, nil
}

// forget drops a cached handle so the file underneath it can be moved.
func (stores *Stores) forget(scope Scope) {
	path, errorValue := stores.Path(scope)
	if errorValue != nil {
		return
	}
	stores.mutex.Lock()
	defer stores.mutex.Unlock()
	if store, isOpen := stores.open[path]; isOpen {
		store.Close()
		delete(stores.open, path)
	}
}

// pathFor is where one scope's file lives, whether or not it exists yet.
// makeHoldingDirectory stands in for the POSIX helper where there is none,
// such as a test. On a host the helper has already made this directory
// setgid to the subject's group, and this call changes nothing.
func makeHoldingDirectory(memoryPath string) error {
	return os.MkdirAll(filepath.Dir(memoryPath), 0o750)
}

func (stores *Stores) Path(scope Scope) (string, error) {
	return scope.pathUnder(stores.workspaceRootPath)
}
