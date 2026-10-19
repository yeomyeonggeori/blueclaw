package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const virtualCRMImportToolName = "crm_record_import"

type virtualCarriedFiles struct {
	Transport struct {
		WorkspaceFiles []struct {
			WorkspacePath string `json:"workspacePath"`
			Filename      string `json:"filename"`
			ContentBase64 string `json:"contentBase64"`
		} `json:"workspaceFiles"`
	} `json:"transport"`
}

type virtualCRMImportFile struct {
	path     string
	filename string
	records  int
}

func virtualCRMImportFileOf(requestBody []byte) (virtualCRMImportFile, bool) {
	var carried virtualCarriedFiles
	if json.Unmarshal(requestBody, &carried) != nil || len(carried.Transport.WorkspaceFiles) != 1 {
		return virtualCRMImportFile{}, false
	}
	file := carried.Transport.WorkspaceFiles[0]
	content, errorValue := base64.StdEncoding.DecodeString(file.ContentBase64)
	if errorValue != nil {
		return virtualCRMImportFile{}, false
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	return virtualCRMImportFile{path: file.WorkspacePath, filename: file.Filename, records: len(lines) - 1}, true
}

func virtualCRMImportTargetResponse(requestBody []byte) string {
	file, isCarried := virtualCRMImportFileOf(requestBody)
	if !isCarried {
		return virtualCapabilityInvalidInput(virtualCRMImportToolName, "the file this import names was not carried with the call")
	}
	return virtualCapabilitySuccess(virtualCRMImportToolName, "resolved virtual import file", map[string]any{
		"inputField": "attachments",
		"ids":        []string{file.path},
		"title":      file.filename,
		"preview":    fmt.Sprintf("creates %d, updates 0, skips 0", file.records),
	})
}

func virtualCRMImportResponse(requestBody []byte) string {
	file, isCarried := virtualCRMImportFileOf(requestBody)
	if !isCarried {
		return virtualCapabilityInvalidInput(virtualCRMImportToolName, "the file this import names was not carried with the call")
	}
	return virtualCapabilitySuccess(virtualCRMImportToolName, fmt.Sprintf("imported %d records", file.records), map[string]any{
		"dryRun":  false,
		"written": true,
		"created": file.records,
		"updated": 0,
		"skipped": 0,
		"refused": 0,
		"rows":    []any{},
	})
}
