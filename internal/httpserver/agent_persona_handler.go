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
	root := firstNonEmptyWorkspaceRoot(handler.WorkspaceRootPath)
	current, errorValue := readInstalledAgentPersona(root)
	if errorValue != nil {
		http.Error(responseWriter, "an existing soul is required for identity updates", http.StatusConflict)
		return
	}
	if string(current.Soul) != string(documents.Soul) {
		http.Error(responseWriter, "agent updates cannot change the soul", http.StatusConflict)
		return
	}
	if errorValue := persona.SaveDocument(root, persona.IdentityFileName, documents.Identity); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if errorValue := removeLegacyAgentDocuments(root); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, documents)
}

func (handler PersonaHandler) HandleReadAgent(responseWriter http.ResponseWriter, request *http.Request) {
	documents, errorValue := readInstalledAgentPersona(firstNonEmptyWorkspaceRoot(handler.WorkspaceRootPath))
	if errorValue != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(errorValue, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(responseWriter, errorValue.Error(), status)
		return
	}
	writeJSON(responseWriter, documents)
}

func (handler PersonaHandler) HandleSeedAgent(responseWriter http.ResponseWriter, request *http.Request) {
	documents, errorValue := readAgentPersonaDocuments(http.MaxBytesReader(responseWriter, request.Body, personaDocumentMaximumBytes))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	root := firstNonEmptyWorkspaceRoot(handler.WorkspaceRootPath)
	for name, document := range map[string]json.RawMessage{persona.IdentityFileName: documents.Identity, persona.SoulFileName: documents.Soul} {
		if errorValue := seedPersonaDocument(root, name, document); errorValue != nil {
			http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
			return
		}
	}
	current, errorValue := readInstalledAgentPersona(root)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(responseWriter, current)
}

func readInstalledAgentPersona(root string) (agentPersonaDocuments, error) {
	identityDocument, errorValue := os.ReadFile(filepath.Join(root, persona.IdentityFileName))
	if errorValue != nil {
		return agentPersonaDocuments{}, errorValue
	}
	soulDocument, errorValue := persona.ReadSoulDocument(root)
	if errorValue != nil {
		return agentPersonaDocuments{}, errorValue
	}
	identity, errorValue := persona.ParseIdentity(identityDocument)
	if errorValue != nil {
		return agentPersonaDocuments{}, errorValue
	}
	soul, errorValue := persona.ParseSoul(soulDocument)
	if errorValue != nil {
		return agentPersonaDocuments{}, errorValue
	}
	canonicalIdentity, errorValue := persona.CanonicalIdentity(identity)
	if errorValue != nil {
		return agentPersonaDocuments{}, errorValue
	}
	canonicalSoul, errorValue := persona.CanonicalSoul(soul)
	if errorValue != nil {
		return agentPersonaDocuments{}, errorValue
	}
	return agentPersonaDocuments{Identity: canonicalIdentity, Soul: canonicalSoul}, nil
}

func seedPersonaDocument(root string, name string, document []byte) error {
	if errorValue := os.MkdirAll(root, 0o750); errorValue != nil {
		return errorValue
	}
	path := filepath.Join(root, name)
	file, errorValue := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(errorValue) {
		return nil
	}
	if errorValue != nil {
		return errorValue
	}
	if _, errorValue := file.Write(document); errorValue != nil {
		file.Close()
		return errorValue
	}
	if errorValue := file.Close(); errorValue != nil {
		return errorValue
	}
	persona.SaveBackup(persona.BackupPath(root, name), document)
	return nil
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
	if errorValue := removeLegacyAgentDocuments(root); errorValue != nil {
		return errorValue
	}
	return nil
}

func removeLegacyAgentDocuments(root string) error {
	for _, name := range []string{"BOT_PROFILE.yaml", "BOT_PROFILE.md", "IDENTITY.md", "SOUL.md"} {
		if errorValue := os.Remove(filepath.Join(root, name)); errorValue != nil && !errors.Is(errorValue, os.ErrNotExist) {
			return errorValue
		}
	}
	return nil
}

func installPersonaDocument(root string, name string, document []byte) error {
	return persona.SaveDocument(root, name, document)
}
