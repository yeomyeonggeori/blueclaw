package migrations

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed *.sql
var Files embed.FS

func LatestVersion() string {
	entries, errorValue := fs.ReadDir(Files, ".")
	if errorValue != nil || len(entries) == 0 {
		return ""
	}
	return strings.TrimSuffix(entries[len(entries)-1].Name(), ".sql")
}
