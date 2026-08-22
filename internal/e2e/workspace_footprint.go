package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A scenario asserts what the agent produced. Nothing asserted what it left behind,
// so a turn that quietly rewrote a fixture, truncated a manifest or emptied a skill
// passed every scenario it was measured on.
func workspaceFileDigests(workspaceRootPath string) (map[string]string, error) {
	digests := map[string]string{}
	walkError := filepath.Walk(workspaceRootPath, func(path string, info os.FileInfo, walkError error) error {
		if walkError != nil || info.IsDir() || !info.Mode().IsRegular() {
			return walkError
		}
		content, readError := os.ReadFile(path)
		if readError != nil {
			return readError
		}
		relativePath, relativeError := filepath.Rel(workspaceRootPath, path)
		if relativeError != nil {
			return relativeError
		}
		sum := sha256.Sum256(content)
		digests[filepath.ToSlash(relativePath)] = hex.EncodeToString(sum[:])
		return nil
	})
	if walkError != nil {
		return nil, walkError
	}
	return digests, nil
}

// Files the scenario created are its work. Files that were already there and are now
// different, or gone, are the ones nobody asked about.
func changedFilesOutsideWritableScope(before map[string]string, after map[string]string, writablePaths []string) []string {
	changes := []string{}
	for relativePath, digest := range before {
		if isWithinWritableScope(relativePath, writablePaths) {
			continue
		}
		afterDigest, isPresent := after[relativePath]
		if !isPresent {
			changes = append(changes, relativePath+" was deleted")
			continue
		}
		if afterDigest != digest {
			changes = append(changes, relativePath+" was rewritten")
		}
	}
	sort.Strings(changes)
	return changes
}

func isWithinWritableScope(relativePath string, writablePaths []string) bool {
	for _, writablePath := range writablePaths {
		trimmedPath := strings.Trim(strings.TrimSpace(filepath.ToSlash(writablePath)), "/")
		if trimmedPath == "" {
			continue
		}
		if relativePath == trimmedPath || strings.HasPrefix(relativePath, trimmedPath+"/") {
			return true
		}
	}
	return false
}
