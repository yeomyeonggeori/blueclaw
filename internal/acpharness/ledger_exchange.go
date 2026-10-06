package acpharness

import (
	"encoding/json"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type ledgerExchange struct {
	skippedEventNames map[string]bool
}

func newLedgerExchange(skippedEventNames []string) *ledgerExchange {
	skipped := make(map[string]bool, len(skippedEventNames))
	for _, eventName := range skippedEventNames {
		skipped[eventName] = true
	}
	return &ledgerExchange{skippedEventNames: skipped}
}

func (harness *Harness) UseLedgerExchange(skippedEventNames []string) {
	harness.ledgerExchange = newLedgerExchange(skippedEventNames)
}

func (exchange *ledgerExchange) replayMeta(taskRunStore taskstate.TaskRunStore, request agentcontract.AgentTurnRequest) map[string]any {
	if exchange == nil || taskRunStore == nil || request.IsTaskRunOpenedForThisTurn || strings.TrimSpace(request.ExistingTaskRunID) == "" {
		return nil
	}
	records := ledgerRecordsOf(taskRunStore.ListTaskEvent(request.ExistingTaskRunID))
	if len(records) == 0 {
		return nil
	}
	return map[string]any{agentcontract.LedgerMetaKey: records}
}

func ledgerRecordsOf(taskEvents []agentcontract.TaskEvent) []agentcontract.LedgerRecord {
	records := make([]agentcontract.LedgerRecord, 0, len(taskEvents))
	for _, taskEvent := range taskEvents {
		records = append(records, agentcontract.LedgerRecord{Name: taskEvent.Name, Body: ledgerBodyOf(taskEvent.Body), Text: taskEvent.Body})
	}
	return records
}

func ledgerBodyOf(eventBody string) json.RawMessage {
	if json.Valid([]byte(eventBody)) {
		return json.RawMessage(eventBody)
	}
	quoted, _ := json.Marshal(eventBody)
	return quoted
}

func ledgerRecordOfUpdate(update acp.SessionUpdate) (agentcontract.LedgerRecord, bool) {
	for _, meta := range updateMetas(update) {
		if record, isRecorded := ledgerRecordOfMeta(meta); isRecorded {
			return record, true
		}
	}
	return agentcontract.LedgerRecord{}, false
}

func updateMetas(update acp.SessionUpdate) []map[string]any {
	switch {
	case update.ToolCall != nil:
		return []map[string]any{update.ToolCall.Meta}
	case update.ToolCallUpdate != nil:
		return []map[string]any{update.ToolCallUpdate.Meta}
	case update.AgentThoughtChunk != nil:
		return []map[string]any{update.AgentThoughtChunk.Meta}
	}
	return nil
}

func ledgerRecordOfMeta(meta map[string]any) (agentcontract.LedgerRecord, bool) {
	value, isPresent := meta[agentcontract.LedgerMetaKey]
	if !isPresent {
		return agentcontract.LedgerRecord{}, false
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return agentcontract.LedgerRecord{}, false
	}
	record := agentcontract.LedgerRecord{}
	if json.Unmarshal(encoded, &record) != nil || strings.TrimSpace(record.Name) == "" {
		return agentcontract.LedgerRecord{}, false
	}
	return record, true
}

type ledgerMirror struct {
	exchange     *ledgerExchange
	taskRunStore taskstate.TaskRunStore
	taskRunID    string
}

func (mirror ledgerMirror) isActive() bool {
	return mirror.exchange != nil && mirror.taskRunStore != nil && strings.TrimSpace(mirror.taskRunID) != ""
}

func (mirror ledgerMirror) take(record agentcontract.LedgerRecord) {
	if !mirror.isActive() || mirror.exchange.skippedEventNames[record.Name] || mirror.hostRecordedTheCancellation(record) {
		return
	}
	mirror.taskRunStore.AppendTaskEvent(mirror.taskRunID, record.Name, record.EventBody())
}

func (mirror ledgerMirror) hostRecordedTheCancellation(record agentcontract.LedgerRecord) bool {
	if !strings.HasPrefix(record.Name, agentcontract.ToolTaskEventPrefix) || !strings.HasSuffix(record.Name, agentcontract.ToolTaskEventCancelledSuffix) {
		return false
	}
	observationID := observationIDOf(record.EventBody())
	for _, taskEvent := range mirror.taskRunStore.ListTaskEvent(mirror.taskRunID) {
		if taskEvent.Name == record.Name && observationIDOf(taskEvent.Body) == observationID {
			return true
		}
	}
	return false
}

func observationIDOf(eventBody string) string {
	document := struct {
		ObservationID string `json:"observationID"`
	}{}
	_ = json.Unmarshal([]byte(eventBody), &document)
	return document.ObservationID
}
