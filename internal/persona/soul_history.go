package persona

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maximumSoulHistory = 100

type SoulRevision struct {
	Version     int             `json:"version"`
	Document    json.RawMessage `json:"document"`
	Reason      string          `json:"reason"`
	EvidenceIDs []string        `json:"evidenceIDs,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	Origin      string          `json:"origin"`
}

var soulHistoryMutex sync.Mutex

func ReadSoulDocument(root string) ([]byte, error) {
	history, errorValue := ReadSoulHistory(root)
	if errorValue != nil {
		return nil, errorValue
	}
	return append([]byte{}, history[len(history)-1].Document...), nil
}

func ReadSoulHistory(root string) ([]SoulRevision, error) {
	path := soulHistoryPath(root)
	document, errorValue := os.ReadFile(path)
	if errorValue == nil {
		var history []SoulRevision
		if errorValue := json.Unmarshal(document, &history); errorValue != nil || len(history) == 0 {
			return nil, errors.New("soul history is invalid")
		}
		return history, nil
	}
	if !os.IsNotExist(errorValue) {
		return nil, errorValue
	}
	baseline, errorValue := os.ReadFile(filepath.Join(root, SoulFileName))
	if os.IsNotExist(errorValue) {
		baseline = []byte(`{"schemaVersion":1}`)
	} else if errorValue != nil {
		return nil, errorValue
	}
	soul, errorValue := ParseSoul(baseline)
	if errorValue != nil {
		return nil, errorValue
	}
	canonical, errorValue := CanonicalSoul(soul)
	if errorValue != nil {
		return nil, errorValue
	}
	return []SoulRevision{{Version: 1, Document: canonical, CreatedAt: time.Now().UTC(), Origin: "initial"}}, nil
}

func AppendSoulRevision(root string, expectedVersion int, document []byte, reason string, evidenceIDs []string, origin string) (SoulRevision, error) {
	soulHistoryMutex.Lock()
	defer soulHistoryMutex.Unlock()
	if expectedVersion <= 0 {
		return SoulRevision{}, errors.New("expected soul revision is required")
	}
	soul, errorValue := ParseSoul(document)
	if errorValue != nil {
		return SoulRevision{}, errorValue
	}
	canonical, errorValue := CanonicalSoul(soul)
	if errorValue != nil {
		return SoulRevision{}, errorValue
	}
	history, errorValue := ReadSoulHistory(root)
	if errorValue != nil {
		return SoulRevision{}, errorValue
	}
	latest := history[len(history)-1]
	if latest.Version != expectedVersion {
		return SoulRevision{}, errors.New("soul revision is stale")
	}
	if len(history) >= maximumSoulHistory {
		history = history[len(history)-maximumSoulHistory+1:]
	}
	revision := SoulRevision{Version: latest.Version + 1, Document: canonical, Reason: reason, EvidenceIDs: append([]string{}, evidenceIDs...), CreatedAt: time.Now().UTC(), Origin: origin}
	history = append(history, revision)
	if errorValue := writeSoulHistory(root, history); errorValue != nil {
		return SoulRevision{}, errorValue
	}
	if errorValue := writeSoulProjection(root, canonical); errorValue != nil {
		return SoulRevision{}, errorValue
	}
	return revision, nil
}

func soulHistoryPath(root string) string {
	return filepath.Join(root, ".blueclaw", "state", "persona", "soul-history.json")
}

func writeSoulHistory(root string, history []SoulRevision) error {
	document, errorValue := json.MarshalIndent(history, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	path := soulHistoryPath(root)
	if errorValue := os.MkdirAll(filepath.Dir(path), 0o700); errorValue != nil {
		return errorValue
	}
	temporary, errorValue := os.CreateTemp(filepath.Dir(path), ".soul-history-*.json")
	if errorValue != nil {
		return errorValue
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, errorValue := temporary.Write(append(document, '\n')); errorValue != nil {
		temporary.Close()
		return errorValue
	}
	if errorValue := temporary.Sync(); errorValue != nil {
		temporary.Close()
		return errorValue
	}
	if errorValue := temporary.Close(); errorValue != nil {
		return errorValue
	}
	return os.Rename(temporaryPath, path)
}

func writeSoulProjection(root string, document []byte) error {
	path := filepath.Join(root, SoulFileName)
	temporary, errorValue := os.CreateTemp(root, ".soul-*.json")
	if errorValue != nil {
		return errorValue
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, errorValue := temporary.Write(append(append([]byte{}, document...), '\n')); errorValue != nil {
		temporary.Close()
		return errorValue
	}
	if errorValue := temporary.Sync(); errorValue != nil {
		temporary.Close()
		return errorValue
	}
	if errorValue := temporary.Close(); errorValue != nil {
		return errorValue
	}
	return os.Rename(temporaryPath, path)
}
