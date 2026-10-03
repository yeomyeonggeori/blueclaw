package memory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluememo"
)

const carriedFileSuffix = ".carrying"

// CarryReport is what one carry forward did.
type CarryReport struct {
	Carried int      `json:"carried"`
	Adopted int      `json:"adopted"`
	Left    []string `json:"left"`
	Removed []string `json:"removed"`
}

// CarryForward brings memory written where blueclaw kept every subject's file
// together into that subject's protected directory. It is
// how a host that wrote memory under the old layout keeps it: nothing else
// looks at the old path, so a start that skipped this would answer from an
// empty memory and say nothing.
//
// A store is checkpointed before it is carried, because a file opened in WAL
// mode holds what was written since its last checkpoint in a sidecar, and
// carrying the database alone would truncate it to that checkpoint rather than
// lose it outright. The carry then copies one file, so no partial move can
// leave a store split across two directories.
//
// The copy is created inside the destination directory, which the POSIX
// synchronizer has already made setgid to the subject, so the new file belongs
// to the group that reads it. Carrying the bytes keeps the embeddings a rename
// would keep and a re-ingest would not.
//
// A file already at the destination is never overwritten. Its store adopts
// what the old file holds, the way two records of one person are merged, so
// memory written at either path is kept.
//
// A circle that holds no directory, such as admin, has nowhere to be carried
// to, so its old file is left where it is and named in the report.
func (stores *Stores) CarryForward(ctx context.Context) (CarryReport, error) {
	report := CarryReport{}
	oldDirectory := filepath.Join(stores.workspaceRootPath, ".blueclaw", "memory")
	for _, scope := range oldLayoutScopes(oldDirectory) {
		oldPath := scope.oldPath
		if scope.scope.Kind == ScopeCircle && !policy.CircleHoldsADirectory(scope.scope.ID) {
			report.Left = append(report.Left, oldPath)
			continue
		}
		destination, errorValue := stores.Path(scope.scope)
		if errorValue != nil {
			return report, fmt.Errorf("where %s belongs now: %w", oldPath, errorValue)
		}
		held, errorValue := holdsFile(destination)
		if errorValue != nil {
			return report, errorValue
		}
		if held {
			if errorValue := stores.adoptFile(ctx, oldPath, scope.scope); errorValue != nil {
				return report, fmt.Errorf("fold %s into %s: %w", oldPath, destination, errorValue)
			}
			report.Adopted++
			continue
		}
		if errorValue := stores.carryOne(ctx, oldPath, destination); errorValue != nil {
			return report, errorValue
		}
		report.Carried++
	}
	if errorValue := stores.removeTheFolderOfACircle(ctx, policy.AdminCircleID, &report); errorValue != nil {
		return report, errorValue
	}
	return report, nil
}

// RemoveRetiredCircleFolders takes away the folders of circles nobody holds
// any more, on the same terms as the admin circle's.
func (stores *Stores) RemoveRetiredCircleFolders(ctx context.Context, circleIDs []string) (CarryReport, error) {
	report := CarryReport{}
	for _, circleID := range circleIDs {
		if errorValue := stores.removeTheFolderOfACircle(ctx, circleID, &report); errorValue != nil {
			return report, errorValue
		}
	}
	return report, nil
}

// removeTheFolderOfACircle takes away a circle's folder. Its store is deleted
// only when it keeps nothing, and the folder only when nothing else is in it;
// otherwise the folder is left and named, so neither a memory nor a file a
// person put there is ever removed.
func (stores *Stores) removeTheFolderOfACircle(ctx context.Context, circleID string, report *CarryReport) error {
	folder := security.CircleDirectoryPath(stores.workspaceRootPath, circleID)
	if _, errorValue := os.Stat(folder); errors.Is(errorValue, os.ErrNotExist) {
		return nil
	} else if errorValue != nil {
		return fmt.Errorf("look at %s: %w", folder, errorValue)
	}
	protected := security.ProtectedDirectoryPath(folder)
	isEmpty, errorValue := stores.storeKeepsNothing(ctx, filepath.Join(protected, memoryFileName))
	if errorValue != nil {
		return fmt.Errorf("look into the memory of the %s circle: %w", circleID, errorValue)
	}
	if !isEmpty {
		report.Left = append(report.Left, folder)
		return nil
	}
	for _, name := range []string{memoryFileName, memoryFileName + "-wal", memoryFileName + "-shm", memoryFileName + carriedFileSuffix} {
		if errorValue := os.Remove(filepath.Join(protected, name)); errorValue != nil && !errors.Is(errorValue, os.ErrNotExist) {
			return fmt.Errorf("remove the memory of the %s circle: %w", circleID, errorValue)
		}
	}
	for _, directory := range []string{protected, folder} {
		entries, errorValue := os.ReadDir(directory)
		if errors.Is(errorValue, os.ErrNotExist) {
			continue
		}
		if errorValue != nil {
			return errorValue
		}
		if len(entries) > 0 {
			report.Left = append(report.Left, directory)
			return nil
		}
		if errorValue := os.Remove(directory); errorValue != nil {
			return fmt.Errorf("remove %s: %w", directory, errorValue)
		}
	}
	report.Removed = append(report.Removed, folder)
	return nil
}

type oldLayoutFile struct {
	oldPath string
	scope   Scope
}

func oldLayoutScopes(oldDirectory string) []oldLayoutFile {
	files := []oldLayoutFile{}
	workspacePath := filepath.Join(oldDirectory, "workspace.db")
	if held, _ := holdsFile(workspacePath); held {
		files = append(files, oldLayoutFile{oldPath: workspacePath, scope: WorkspaceScope()})
	}
	for directory, scopeOf := range map[string]func(string) Scope{"persons": PersonScope, "circles": CircleScope} {
		entries, errorValue := os.ReadDir(filepath.Join(oldDirectory, directory))
		if errorValue != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
				continue
			}
			subject := strings.TrimSuffix(entry.Name(), ".db")
			files = append(files, oldLayoutFile{
				oldPath: filepath.Join(oldDirectory, directory, entry.Name()),
				scope:   scopeOf(subject),
			})
		}
	}
	return files
}

func (stores *Stores) carryOne(ctx context.Context, oldPath string, destination string) error {
	if errorValue := checkpoint(ctx, oldPath, stores.configuration); errorValue != nil {
		return fmt.Errorf("settle what %s holds in its log before carrying it: %w", oldPath, errorValue)
	}
	if errorValue := os.MkdirAll(filepath.Dir(destination), 0o700); errorValue != nil {
		return fmt.Errorf("make room for %s: %w", destination, errorValue)
	}
	if errorValue := copyFile(oldPath, destination); errorValue != nil {
		return errorValue
	}
	return removeStoreFile(oldPath)
}

// removeStoreFile takes away a store file whose memory now lives elsewhere,
// with the sidecars a WAL-mode file may have left beside it.
func removeStoreFile(path string) error {
	for _, sidecar := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + sidecar)
	}
	if errorValue := os.Remove(path); errorValue != nil {
		return fmt.Errorf("take %s away once its memory lives elsewhere: %w", path, errorValue)
	}
	return nil
}

// checkpoint opens a store only so that closing it folds what its log holds
// into the database, leaving one file to carry.
func (stores *Stores) storeKeepsNothing(ctx context.Context, path string) (bool, error) {
	if held, errorValue := holdsFile(path); errorValue != nil || !held {
		return errorValue == nil, errorValue
	}
	store, errorValue := bluememo.Open(ctx, path, stores.configuration)
	if errorValue != nil {
		return false, errorValue
	}
	defer store.Close()
	return store.IsEmpty(ctx)
}

func checkpoint(ctx context.Context, path string, configuration bluememo.Configuration) error {
	store, errorValue := bluememo.Open(ctx, path, configuration)
	if errorValue != nil {
		return errorValue
	}
	return store.Close()
}

func copyFile(from string, to string) error {
	source, errorValue := os.Open(from)
	if errorValue != nil {
		return fmt.Errorf("read %s: %w", from, errorValue)
	}
	defer source.Close()
	carrying := to + carriedFileSuffix
	destination, errorValue := os.OpenFile(carrying, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if errorValue != nil {
		return fmt.Errorf("write %s: %w", carrying, errorValue)
	}
	if _, errorValue = io.Copy(destination, source); errorValue != nil {
		destination.Close()
		_ = os.Remove(carrying)
		return fmt.Errorf("carry %s to %s: %w", from, to, errorValue)
	}
	if errorValue = destination.Sync(); errorValue != nil {
		destination.Close()
		_ = os.Remove(carrying)
		return fmt.Errorf("put %s on disk: %w", carrying, errorValue)
	}
	if errorValue = destination.Close(); errorValue != nil {
		_ = os.Remove(carrying)
		return fmt.Errorf("close %s: %w", carrying, errorValue)
	}
	if errorValue = os.Chmod(carrying, 0o640); errorValue != nil {
		_ = os.Remove(carrying)
		return fmt.Errorf("let the subject's group read %s: %w", carrying, errorValue)
	}
	if errorValue = os.Rename(carrying, to); errorValue != nil {
		_ = os.Remove(carrying)
		return fmt.Errorf("put %s in place: %w", to, errorValue)
	}
	return nil
}

func holdsFile(path string) (bool, error) {
	information, errorValue := os.Stat(path)
	if errors.Is(errorValue, os.ErrNotExist) {
		return false, nil
	}
	if errorValue != nil {
		return false, fmt.Errorf("look at %s: %w", path, errorValue)
	}
	return !information.IsDir(), nil
}
