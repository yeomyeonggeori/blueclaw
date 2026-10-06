package connectors

import (
	"strings"
	"sync"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const sentAttachmentSourceCapacity = 512

type sentAttachmentSourceStore struct {
	mutex       sync.Mutex
	pathsByKey  map[string]string
	orderedKeys []string
}

func newSentAttachmentSourceStore() *sentAttachmentSourceStore {
	return &sentAttachmentSourceStore{pathsByKey: map[string]string{}}
}

func (store *sentAttachmentSourceStore) RecordReply(platform string, messageID string, attachments []toolcontract.FileAttachment) {
	if store == nil || strings.TrimSpace(platform) == "" || strings.TrimSpace(messageID) == "" {
		return
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for filename, devicePath := range unambiguousAttachmentSourcePaths(attachments) {
		store.record(sentAttachmentSourceKey(platform, messageID, filename), devicePath)
	}
}

func (store *sentAttachmentSourceStore) SourcePath(platform string, messageID string, filename string) (string, bool) {
	if store == nil {
		return "", false
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	devicePath, isFound := store.pathsByKey[sentAttachmentSourceKey(platform, messageID, filename)]
	return devicePath, isFound
}

func (store *sentAttachmentSourceStore) record(key string, devicePath string) {
	if _, isKnown := store.pathsByKey[key]; !isKnown {
		store.orderedKeys = append(store.orderedKeys, key)
	}
	store.pathsByKey[key] = devicePath
	for len(store.orderedKeys) > sentAttachmentSourceCapacity {
		oldestKey := store.orderedKeys[0]
		store.orderedKeys = store.orderedKeys[1:]
		delete(store.pathsByKey, oldestKey)
	}
}

func unambiguousAttachmentSourcePaths(attachments []toolcontract.FileAttachment) map[string]string {
	pathsByFilename := map[string]string{}
	ambiguousFilenames := map[string]bool{}
	for _, attachment := range attachments {
		filename := strings.TrimSpace(attachment.Filename)
		devicePath := strings.TrimSpace(attachment.DevicePath)
		if filename == "" || devicePath == "" {
			continue
		}
		if _, isKnown := pathsByFilename[filename]; isKnown {
			ambiguousFilenames[filename] = true
			continue
		}
		pathsByFilename[filename] = devicePath
	}
	for filename := range ambiguousFilenames {
		delete(pathsByFilename, filename)
	}
	return pathsByFilename
}

func sentAttachmentSourceKey(platform string, messageID string, filename string) string {
	return strings.TrimSpace(platform) + "\x00" + strings.TrimSpace(messageID) + "\x00" + strings.TrimSpace(filename)
}
