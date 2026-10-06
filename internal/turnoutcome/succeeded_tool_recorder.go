package turnoutcome

import (
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type SucceededToolRecorder struct {
	mutex       sync.Mutex
	toolNames   []string
	attachments []toolcontract.FileAttachment
}

func (recorder *SucceededToolRecorder) Observe(toolName string, toolResult toolcontract.ToolResult, isSucceeded bool) {
	if !isSucceeded {
		return
	}
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.recordToolName(toolName)
	recorder.recordAttachments(toolResult.Attachments)
}

func (recorder *SucceededToolRecorder) recordToolName(toolName string) {
	for _, recordedToolName := range recorder.toolNames {
		if recordedToolName == toolName {
			return
		}
	}
	recorder.toolNames = append(recorder.toolNames, toolName)
}

func (recorder *SucceededToolRecorder) recordAttachments(attachments []toolcontract.FileAttachment) {
	for _, attachment := range attachments {
		if !recorder.hasAttachment(attachment) {
			recorder.attachments = append(recorder.attachments, attachment)
		}
	}
}

func (recorder *SucceededToolRecorder) hasAttachment(candidate toolcontract.FileAttachment) bool {
	candidatePath := strings.TrimSpace(candidate.DevicePath)
	if candidatePath == "" {
		return true
	}
	for _, attachment := range recorder.attachments {
		if strings.TrimSpace(attachment.DevicePath) == candidatePath {
			return true
		}
	}
	return false
}

func (recorder *SucceededToolRecorder) SucceededToolNames() []string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]string{}, recorder.toolNames...)
}

func (recorder *SucceededToolRecorder) StagedAttachments() []toolcontract.FileAttachment {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]toolcontract.FileAttachment{}, recorder.attachments...)
}
