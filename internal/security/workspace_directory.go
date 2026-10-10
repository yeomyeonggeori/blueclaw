package security

import (
	"os"
	"path/filepath"
)

// ReadWorkspaceDirectory lists a directory as the running process. A
// subdirectory's EntryCount is unset when that process cannot read it.
func ReadWorkspaceDirectory(path string) ([]WorkspaceActorDirectoryEntry, error) {
	directoryEntries, errorValue := os.ReadDir(path)
	if errorValue != nil {
		return nil, errorValue
	}
	entries := []WorkspaceActorDirectoryEntry{}
	for _, directoryEntry := range directoryEntries {
		entryPath := filepath.Join(path, directoryEntry.Name())
		fileInformation, errorValue := os.Stat(entryPath)
		if errorValue != nil {
			continue
		}
		entry := WorkspaceActorDirectoryEntry{
			Name:           directoryEntry.Name(),
			IsDirectory:    fileInformation.IsDir(),
			SizeBytes:      fileInformation.Size(),
			ModifiedAtUnix: fileInformation.ModTime().Unix(),
		}
		if entry.IsDirectory {
			entry.EntryCount = directoryEntryCount(entryPath)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func directoryEntryCount(path string) *int {
	directory, errorValue := os.Open(path)
	if errorValue != nil {
		return nil
	}
	defer directory.Close()
	names, errorValue := directory.Readdirnames(-1)
	if errorValue != nil {
		return nil
	}
	count := len(names)
	return &count
}
