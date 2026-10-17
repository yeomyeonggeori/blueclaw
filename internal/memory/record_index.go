package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluememo"
)

const (
	recordIndexFileMode  = 0o600
	recordIndexScanLimit = 5000
	recordFrameSuffix    = "#frame"
)

type RecordIndex struct {
	stores  *Stores
	mutex   sync.Mutex
	opened  *bluememo.Store
	indexed map[string]string
}

type RecordEntry struct {
	ID        string
	Content   string
	ChangedAt string
}

type RecordRise struct {
	ID   string
	Rise float64
}

func (stores *Stores) RecordIndex() *RecordIndex {
	stores.mutex.Lock()
	defer stores.mutex.Unlock()
	if stores.recordIndex == nil {
		stores.recordIndex = &RecordIndex{stores: stores, indexed: map[string]string{}}
	}
	return stores.recordIndex
}

func (index *RecordIndex) Hold(ctx context.Context, source string, entry RecordEntry) error {
	fileID := recordFileID(source, entry.ID)
	if index.isHeld(fileID, entry.ChangedAt) {
		return nil
	}
	if errorValue := index.storeFile(ctx, fileID, entry.ID, entry.Content); errorValue != nil {
		return errorValue
	}
	index.markHeld(fileID, entry.ChangedAt)
	return nil
}

func (index *RecordIndex) HoldFrame(ctx context.Context, source string, frame string) error {
	fileID := source + recordFrameSuffix
	if index.isHeld(fileID, frame) {
		return nil
	}
	if errorValue := index.storeFile(ctx, fileID, fileID, frame); errorValue != nil {
		return errorValue
	}
	index.markHeld(fileID, frame)
	return nil
}

func (index *RecordIndex) Nearest(ctx context.Context, source string, query string, minimumRise float64, limit int) ([]RecordRise, error) {
	store, errorValue := index.store(ctx)
	if errorValue != nil {
		return nil, errorValue
	}
	ranked, errorValue := store.RecallFiles(ctx, bluememo.FileRequest{Text: query, Limit: recordIndexScanLimit})
	if errorValue != nil {
		return nil, errorValue
	}
	frame, hasFrame := frameRelevance(ranked, source)
	if !hasFrame {
		return nil, nil
	}
	rises := []RecordRise{}
	prefix := source + "/"
	for _, recalled := range ranked {
		rise := recalled.Relevance - frame
		if !strings.HasPrefix(recalled.File.FileID, prefix) || rise < minimumRise {
			continue
		}
		rises = append(rises, RecordRise{ID: strings.TrimPrefix(recalled.File.FileID, prefix), Rise: rise})
		if len(rises) == limit {
			break
		}
	}
	return rises, nil
}

func frameRelevance(ranked []bluememo.RecalledFile, source string) (float64, bool) {
	for _, recalled := range ranked {
		if recalled.File.FileID == source+recordFrameSuffix {
			return recalled.Relevance, true
		}
	}
	return 0, false
}

func recordFileID(source string, id string) string {
	return source + "/" + id
}

func (index *RecordIndex) isHeld(fileID string, version string) bool {
	index.mutex.Lock()
	defer index.mutex.Unlock()
	held, isHeld := index.indexed[fileID]
	return isHeld && held == version
}

func (index *RecordIndex) markHeld(fileID string, version string) {
	index.mutex.Lock()
	defer index.mutex.Unlock()
	index.indexed[fileID] = version
}

func (index *RecordIndex) storeFile(ctx context.Context, fileID string, name string, content string) error {
	store, errorValue := index.store(ctx)
	if errorValue != nil {
		return errorValue
	}
	return store.StoreFile(ctx, bluememo.File{FileID: fileID, Name: name, Medium: bluememo.MediumDocument, Summary: content})
}

func (index *RecordIndex) store(ctx context.Context) (*bluememo.Store, error) {
	index.mutex.Lock()
	defer index.mutex.Unlock()
	if index.opened != nil {
		return index.opened, nil
	}
	if strings.TrimSpace(index.stores.workspaceRootPath) == "" {
		return nil, errors.New("the record index has no workspace root to sit under")
	}
	path := filepath.Join(index.stores.workspaceRootPath, ".blueclaw", "state", "record-index.db")
	if errorValue := makeHoldingDirectory(path); errorValue != nil {
		return nil, errorValue
	}
	opened, errorValue := bluememo.Open(ctx, path, index.stores.configuration)
	if errorValue != nil {
		return nil, errorValue
	}
	if errorValue := os.Chmod(path, recordIndexFileMode); errorValue != nil {
		_ = opened.Close()
		return nil, fmt.Errorf("keep the record index to the service at %s: %w", path, errorValue)
	}
	index.opened = opened
	return opened, nil
}

func (index *RecordIndex) Close() error {
	index.mutex.Lock()
	defer index.mutex.Unlock()
	if index.opened == nil {
		return nil
	}
	errorValue := index.opened.Close()
	index.opened = nil
	return errorValue
}
