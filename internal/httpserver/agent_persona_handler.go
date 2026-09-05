package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
)

type agentPersonaDocuments struct {
	Identity json.RawMessage `json:"identity"`
	Soul     json.RawMessage `json:"soul"`
}

func (handler PersonaHandler) HandleWriteAgent(responseWriter http.ResponseWriter, request *http.Request) {
	documents, errorValue := readAgentPersonaDocuments(http.MaxBytesReader(responseWriter, request.Body, personaDocumentMaximumBytes))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if errorValue := installAgentPersona(firstNonEmptyWorkspaceRoot(handler.WorkspaceRootPath), documents); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, documents)
}

func readAgentPersonaDocuments(reader io.Reader) (agentPersonaDocuments, error) {
	var documents agentPersonaDocuments
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if errorValue := decoder.Decode(&documents); errorValue != nil {
		return documents, errorValue
	}
	if errorValue := decoder.Decode(new(json.RawMessage)); !errors.Is(errorValue, io.EOF) {
		return documents, errors.New("expected one persona document bundle")
	}
	identity, errorValue := persona.ParseIdentity(documents.Identity)
	if errorValue != nil {
		return documents, errorValue
	}
	soul, errorValue := persona.ParseSoul(documents.Soul)
	if errorValue != nil {
		return documents, errorValue
	}
	documents.Identity, errorValue = persona.CanonicalIdentity(identity)
	if errorValue != nil {
		return documents, errorValue
	}
	documents.Soul, errorValue = persona.CanonicalSoul(soul)
	return documents, errorValue
}

func installAgentPersona(root string, documents agentPersonaDocuments) error {
	for name, document := range map[string]json.RawMessage{persona.IdentityFileName: documents.Identity, persona.SoulFileName: documents.Soul} {
		if errorValue := installPersonaDocument(root, name, document); errorValue != nil {
			return errorValue
		}
	}
	for _, name := range []string{"BOT_PROFILE.yaml", "BOT_PROFILE.md", "IDENTITY.md", "SOUL.md"} {
		if errorValue := os.Remove(filepath.Join(root, name)); errorValue != nil && !errors.Is(errorValue, os.ErrNotExist) {
			return errorValue
		}
	}
	return nil
}

func installPersonaDocument(root string, name string, document []byte) error {
	if errorValue := os.MkdirAll(root, 0o750); errorValue != nil {
		return errorValue
	}
	file, errorValue := os.CreateTemp(root, ".persona-*.json")
	if errorValue != nil {
		return errorValue
	}
	defer os.Remove(file.Name())
	if _, errorValue := file.Write(document); errorValue != nil {
		file.Close()
		return errorValue
	}
	if errorValue := file.Close(); errorValue != nil {
		return errorValue
	}
	if errorValue := os.Chmod(file.Name(), 0o644); errorValue != nil {
		return errorValue
	}
	if errorValue := os.Rename(file.Name(), filepath.Join(root, name)); errorValue != nil {
		return errorValue
	}
	persona.SaveBackup(persona.BackupPath(root, name), document)
	return nil
}
