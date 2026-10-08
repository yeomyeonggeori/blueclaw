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

// pathUnder puts a subject's memory in that subject's protected directory,
// where the permissions the workspace already declares are what decide who
// opens it.
func (scope Scope) pathUnder(workspaceRootPath string) (string, error) {
	if strings.TrimSpace(workspaceRootPath) == "" {
		return "", errors.New("memory has no workspace root to sit under")
	}
	directory := scope.protectedDirectoryUnder(workspaceRootPath)
	if directory == "" {
		return "", fmt.Errorf("memory scope %q with identifier %q names no directory", scope.Kind, scope.ID)
	}
	return filepath.Join(directory, memoryFileName), nil
}

func (scope Scope) protectedDirectoryUnder(workspaceRootPath string) string {
	switch scope.Kind {
	case ScopePerson:
		return security.PersonProtectedDirectoryPath(workspaceRootPath, scope.ID)
	case ScopeCircle:
		return security.ProtectedDirectoryPath(security.CircleDirectoryPath(workspaceRootPath, scope.ID))
	case ScopeWorkspace:
		return security.ProtectedDirectoryPath(security.SharedDirectoryPath(workspaceRootPath))
	}
	return ""
}

// Stores holds the memory files this company's agent reads and writes, opening
// each on first use and keeping it for the life of the process.
//
// A recall runs as the person whose memory it is, so the kernel refuses the
// files their identity cannot open. Everything else here runs as the service:
// a subject's memory is kept on their behalf, which is why they read it and
// do not write it.
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

// Store opens one subject's file as the service, which is how a memory is
// written on their behalf. A recall does not come through here: it runs as
// the person, so that the kernel and not this process decides what opens.
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

// ScopeToRemember is where what a person says is written: the circle the
// conversation belongs to when it belongs to one that has a directory, and the
// person's own memory otherwise.
func ScopeToRemember(personID string, activeCircleID string) Scope {
	if policy.CircleHoldsADirectory(activeCircleID) {
		return CircleScope(strings.ToLower(strings.TrimSpace(activeCircleID)))
	}
	return PersonScope(personID)
}

// ScopesToSearch says where to look for a reader's memory, nearest first. It grants nothing:
// a recall runs as that reader, so a file this list names and their identity
// cannot open is refused by the kernel. A list too generous costs a refusal,
// and a list too narrow costs a memory nobody finds.
func ScopesToSearch(personAccess policy.PersonAccess, containedCircles map[string][]string) []Scope {
	scopes := []Scope{}
	if personAccess.PersonID != "" {
		scopes = append(scopes, PersonScope(personAccess.PersonID))
	}
	seen := map[string]bool{}
	for _, circleID := range personAccess.Circles {
		for _, reachable := range append([]string{circleID}, containedCircles[circleID]...) {
			if !policy.CircleHoldsADirectory(reachable) || seen[reachable] {
				continue
			}
			seen[reachable] = true
			scopes = append(scopes, CircleScope(reachable))
		}
	}
	return append(scopes, WorkspaceScope())
}

// RecallAcross returns what the agent loop reads: the scopes are a stack, the
// person's own memory on top, and the read goes through all of it at once with
// what is nearer the top first. It reads as the person the recall is for, so a
// file their identity cannot open is refused by the kernel rather than by a
// list this code keeps. A scope with no file yet has nothing to say, which is
// not a failure, while a file that exists and will not open is.
func (stores *Stores) RecallAcross(ctx context.Context, personAccess policy.PersonAccess, scopes []Scope, query string, limit int) (Recalled, error) {
	recalled, recalledIDs, errorValue := stores.readThrough(ctx, personAccess, scopes, query, limit)
	if errorValue != nil {
		return recalled, errorValue
	}
	return recalled, stores.reinforce(ctx, recalledIDs)
}

// PreviewAcross reads the stack the way RecallAcross does and leaves every
// file as it was: a person looking up what would come to mind is not the agent
// using a memory, so nothing is reinforced.
func (stores *Stores) PreviewAcross(ctx context.Context, personAccess policy.PersonAccess, scopes []Scope, query string, limit int) (Recalled, error) {
	recalled, _, errorValue := stores.readThrough(ctx, personAccess, scopes, query, limit)
	return recalled, errorValue
}

func (stores *Stores) readThrough(ctx context.Context, personAccess policy.PersonAccess, scopes []Scope, query string, limit int) (Recalled, map[Scope][]string, error) {
	recalled := Recalled{Mode: "merged"}
	held, errorValue := stores.Held(ctx, scopes)
	if errorValue != nil || len(held) == 0 {
		return recalled, nil, errorValue
	}
	result, errorValue := stores.readAs(ctx, personAccess, held, query, limit)
	if errorValue != nil {
		return recalled, nil, errorValue
	}
	recalled.DegradedReason = result.Recall.DegradedReason
	recalledIDs := map[Scope][]string{}
	for index, entry := range result.Recall.Memories {
		scope := held[result.Layers[index]]
		recalled.Facts = append(recalled.Facts, memoryFactFrom(entry, scope))
		recalledIDs[scope] = append(recalledIDs[scope], entry.Memory.MemoryID)
	}
	return recalled, recalledIDs, nil
}

// Held opens, as their keeper, the scopes that already hold a file, because a
// reader that may not write a store reads it only while its keeper holds it
// open. A scope with no file is left out rather than created.
func (stores *Stores) Held(ctx context.Context, scopes []Scope) ([]Scope, error) {
	held := []Scope{}
	for _, scope := range scopes {
		path, errorValue := stores.Path(scope)
		if errorValue != nil {
			return nil, errorValue
		}
		if _, errorValue := os.Stat(path); errors.Is(errorValue, os.ErrNotExist) {
			continue
		}
		if _, errorValue := stores.Store(ctx, scope); errorValue != nil {
			return nil, errorValue
		}
		held = append(held, scope)
	}
	return held, nil
}

func (stores *Stores) reinforce(ctx context.Context, recalledIDs map[Scope][]string) error {
	for scope, memoryIDs := range recalledIDs {
		store, errorValue := stores.Store(ctx, scope)
		if errorValue != nil {
			return errorValue
		}
		if errorValue := store.Reinforce(ctx, memoryIDs); errorValue != nil {
			return fmt.Errorf("reinforce what was recalled from %s: %w", scope.Kind, errorValue)
		}
	}
	return nil
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
		ScopeID:         scope.ID,
		Content:         entry.Memory.Content,
		Score:           entry.Score,
		SourceEpisodeID: entry.Memory.OriginID,
		SourceKind:      sourceKind,
		ValidAt:         validAt,
	}
}

// StackToRemember is the scope a statement is written into followed by the
// layers it stands on: every searched scope broader than it. A person's memory
// stands on their circles' and the company's, and a circle's on the company's.
func StackToRemember(target Scope, searched []Scope) []Scope {
	stack := []Scope{target}
	for _, scope := range searched {
		if breadth(scope) > breadth(target) {
			stack = append(stack, scope)
		}
	}
	return stack
}

func breadth(scope Scope) int {
	return map[string]int{ScopePerson: 0, ScopeCircle: 1, ScopeWorkspace: 2}[scope.Kind]
}

// Remember takes what was said into the top of a stack and settles it there,
// so the caller's next recall can see it. What a layer beneath already knows
// is not written again, including any layer the caller adds beneath the stack.
func (stores *Stores) Remember(ctx context.Context, stack []Scope, note bluememo.Note, alsoBeneath ...bluememo.Known) (bluememo.SettleReport, error) {
	if len(stack) == 0 {
		return bluememo.SettleReport{}, errors.New("memory has no scope to remember into")
	}
	store, errorValue := stores.Store(ctx, stack[0])
	if errorValue != nil {
		return bluememo.SettleReport{}, errorValue
	}
	beneath, errorValue := stores.layers(ctx, stack[1:])
	if errorValue != nil {
		return bluememo.SettleReport{}, errorValue
	}
	layered := store.On(append(beneath, alsoBeneath...)...)
	if errorValue := layered.Memorize(ctx, note); errorValue != nil {
		return bluememo.SettleReport{}, errorValue
	}
	return layered.Settle(ctx)
}

func (stores *Stores) layers(ctx context.Context, scopes []Scope) ([]bluememo.Known, error) {
	held, errorValue := stores.Held(ctx, scopes)
	if errorValue != nil {
		return nil, errorValue
	}
	layers := make([]bluememo.Known, 0, len(held))
	for _, scope := range held {
		store, errorValue := stores.Store(ctx, scope)
		if errorValue != nil {
			return nil, errorValue
		}
		layers = append(layers, store)
	}
	return layers, nil
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
// files they searched it is held. It fails closed on an identifier no search
// of theirs surfaced, so a reader can only forget what they were shown.
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

type MaintenanceReport struct {
	Scopes     int
	Reembedded bluememo.ReembedReport
}

// Maintain sweeps what has gone cold and re-embeds what a changed embedding
// model left stale, across every memory file on disk rather than only the ones
// this process has already opened. A scope that fails does not hold back the
// others; their failures come back together.
func (stores *Stores) Maintain(ctx context.Context) (MaintenanceReport, error) {
	scopes, errorValue := stores.storedScopes()
	if errorValue != nil {
		return MaintenanceReport{}, errorValue
	}
	report := MaintenanceReport{}
	failures := []error{}
	for _, scope := range scopes {
		reembedded, errorValue := stores.maintainScope(ctx, scope)
		if errorValue != nil {
			failures = append(failures, fmt.Errorf("%s %s: %w", scope.Kind, scope.ID, errorValue))
			continue
		}
		report.Scopes++
		report.Reembedded.Memories += reembedded.Memories
		report.Reembedded.Triggers += reembedded.Triggers
		report.Reembedded.Files += reembedded.Files
	}
	return report, errors.Join(failures...)
}

func (stores *Stores) maintainScope(ctx context.Context, scope Scope) (bluememo.ReembedReport, error) {
	store, errorValue := stores.Store(ctx, scope)
	if errorValue != nil {
		return bluememo.ReembedReport{}, errorValue
	}
	if _, errorValue := store.Sweep(ctx); errorValue != nil {
		return bluememo.ReembedReport{}, errorValue
	}
	return store.Reembed(ctx, reembedBatchSize)
}

// storedScopes lists the scopes that hold a file, by the directories their
// subjects live in. A directory whose name names no subject holds no memory.
func (stores *Stores) storedScopes() ([]Scope, error) {
	candidates := []Scope{WorkspaceScope()}
	for _, subjects := range []struct {
		directory string
		scopeOf   func(string) Scope
	}{
		{security.PeopleProtectedDirectoryPath(stores.workspaceRootPath), PersonScope},
		{security.CirclesDirectoryPath(stores.workspaceRootPath), CircleScope},
	} {
		entries, errorValue := os.ReadDir(subjects.directory)
		if errors.Is(errorValue, os.ErrNotExist) {
			continue
		}
		if errorValue != nil {
			return nil, errorValue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				candidates = append(candidates, subjects.scopeOf(entry.Name()))
			}
		}
	}
	stored := []Scope{}
	for _, scope := range candidates {
		path, errorValue := stores.Path(scope)
		if errorValue != nil {
			continue
		}
		if _, errorValue := os.Stat(path); errorValue == nil {
			stored = append(stored, scope)
		}
	}
	return stored, nil
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
	return stores.adoptFile(ctx, fromPath, PersonScope(toPersonID))
}

// adoptFile moves every memory one store file holds into a scope's store and
// then takes the file away.
func (stores *Stores) adoptFile(ctx context.Context, fromPath string, into Scope) error {
	memories, errorValue := memoriesIn(ctx, fromPath, stores.configuration)
	if errorValue != nil {
		return errorValue
	}
	surviving, errorValue := stores.Store(ctx, into)
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
	return removeStoreFile(fromPath)
}

func memoriesIn(ctx context.Context, path string, configuration bluememo.Configuration) ([]bluememo.Memory, error) {
	store, errorValue := bluememo.Open(ctx, path, configuration)
	if errorValue != nil {
		return nil, errorValue
	}
	memories, errorValue := store.Memories(ctx)
	return memories, errors.Join(errorValue, store.Close())
}

// readAs embeds the query once, because the service holds the credentials for
// that, and reads the stack under the reader's own POSIX identity.
func (stores *Stores) readAs(ctx context.Context, personAccess policy.PersonAccess, scopes []Scope, query string, limit int) (ReadResult, error) {
	vector, errorValue := stores.configuration.Embedder.EmbedQuery(ctx, query)
	if errorValue != nil {
		return ReadResult{}, fmt.Errorf("embed the recall query: %w", errorValue)
	}
	paths := make([]string, len(scopes))
	for index, scope := range scopes {
		if paths[index], errorValue = stores.Path(scope); errorValue != nil {
			return ReadResult{}, errorValue
		}
	}
	request, errorValue := json.Marshal(ReadRequest{
		StorePaths:     paths,
		Query:          query,
		QueryVector:    vector,
		EmbeddingModel: stores.configuration.EmbeddingModel,
		Limit:          limit,
		LaneDepth:      stores.configuration.LaneDepth,
		RecallSources:  stores.configuration.RecallSources,
	})
	if errorValue != nil {
		return ReadResult{}, errorValue
	}
	actor, errorValue := stores.actor.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      personAccess,
		WorkspaceRootPath: stores.workspaceRootPath,
	})
	if errorValue != nil {
		return ReadResult{}, fmt.Errorf("read memory as %s: %w", personAccess.PersonID, errorValue)
	}
	executablePath, errorValue := os.Executable()
	if errorValue != nil {
		return ReadResult{}, fmt.Errorf("find the binary that serves a read: %w", errorValue)
	}
	return runRead(ctx, actor, executablePath, request, len(paths))
}

func runRead(ctx context.Context, actor security.WorkspaceActor, executablePath string, request []byte, layerCount int) (ReadResult, error) {
	result, errorValue := actor.Run(ctx, security.CommandRequest{
		ExecutableName:     executablePath,
		Arguments:          []string{ReadCommand},
		Stdin:              string(request),
		TimeoutSecond:      readTimeoutSecond,
		OutputMaximumBytes: readOutputMaximumBytes,
	})
	if errorValue != nil {
		return ReadResult{}, errorValue
	}
	if result.ExitCode != 0 {
		return ReadResult{}, fmt.Errorf("a read refused: %s", strings.TrimSpace(result.Stderr))
	}
	var read ReadResult
	if errorValue := json.Unmarshal([]byte(result.Stdout), &read); errorValue != nil {
		return ReadResult{}, fmt.Errorf("decode what a read answered: %w", errorValue)
	}
	return read, read.validate(layerCount)
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
