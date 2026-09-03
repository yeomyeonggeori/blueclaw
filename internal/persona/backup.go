package persona

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
)

func BackupPath(rootPath string, fileName string) string {
	return filepath.Join(rootPath, ".blueclaw", "state", "persona-backup", fileName)
}

func UserBackupPath(workspaceRootPath string, personID string) string {
	return filepath.Join(workspaceRootPath, ".blueclaw", "state", "persona-backup", "people", personID, "user.json")
}

func ParseWithBackup[Document any](parse func([]byte) (Document, error), document []byte, backupPath string) (Document, []byte, bool, error) {
	parsed, parseError := parse(document)
	if parseError == nil {
		SaveBackup(backupPath, document)
		return parsed, document, false, nil
	}
	backup, readError := os.ReadFile(backupPath)
	if readError != nil {
		var empty Document
		return empty, document, false, parseError
	}
	restored, backupError := parse(backup)
	if backupError != nil {
		var empty Document
		return empty, document, false, parseError
	}
	return restored, backup, true, nil
}

func SaveBackup(backupPath string, document []byte) {
	if existing, errorValue := os.ReadFile(backupPath); errorValue == nil && bytes.Equal(existing, document) {
		return
	}
	if errorValue := os.MkdirAll(filepath.Dir(backupPath), 0o700); errorValue != nil {
		slog.Warn("persona.backup_save_failed", "path", backupPath, "error", errorValue.Error())
		return
	}
	temporaryPath := backupPath + ".tmp"
	if errorValue := os.WriteFile(temporaryPath, document, 0o600); errorValue != nil {
		slog.Warn("persona.backup_save_failed", "path", backupPath, "error", errorValue.Error())
		return
	}
	if errorValue := os.Rename(temporaryPath, backupPath); errorValue != nil {
		slog.Warn("persona.backup_save_failed", "path", backupPath, "error", errorValue.Error())
	}
}
