// Package sessionquery reads the task ledger back.
//
// The ledger is the best record this product keeps and it was write-only: reading it
// meant one admin call for one task run whose ID you already had. Nobody could ask
// when something last worked, or what happened the last three times a person asked
// for it.
package sessionquery

import (
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const (
	defaultLimit = 20
	maximumLimit = 100
)

type Store interface {
	ListTaskRunByPersonID(personID string) []task.TaskRun
	ListTaskEvent(taskRunID string) []task.TaskEvent
}

type Request struct {
	RequesterPersonID string
	Text              string
	ConversationID    string
	Limit             int
}

type Match struct {
	TaskRunID      string    `json:"taskRunID"`
	ConversationID string    `json:"conversationID,omitempty"`
	Status         string    `json:"status"`
	Prompt         string    `json:"prompt"`
	CreatedAt      time.Time `json:"createdAt"`
	MatchedIn      []string  `json:"matchedIn,omitempty"`
}

type Result struct {
	Matches      []Match `json:"matches"`
	TotalMatched int     `json:"totalMatched"`
	Scanned      int     `json:"scanned"`
	IsTruncated  bool    `json:"isTruncated"`
}

type Service struct {
	store Store
}

func New(store Store) Service {
	return Service{store: store}
}

// A person is required. The ledger holds message text, file contents and tool inputs
// from everyone on this device, so a search that names nobody is a search of all of it.
func (service Service) Search(request Request) (Result, error) {
	personID := strings.TrimSpace(request.RequesterPersonID)
	if personID == "" {
		return Result{}, errors.New("a ledger search names the person whose tasks it may read")
	}
	if service.store == nil {
		return Result{}, errors.New("no task store is configured")
	}
	limit := boundedLimit(request.Limit)
	result := Result{Matches: []Match{}}
	for _, taskRun := range service.store.ListTaskRunByPersonID(personID) {
		if !matchesConversation(taskRun, request.ConversationID) {
			continue
		}
		result.Scanned++
		matchedIn := service.matchedFields(taskRun, request.Text)
		if matchedIn == nil {
			continue
		}
		result.TotalMatched++
		if len(result.Matches) < limit {
			result.Matches = append(result.Matches, matchFor(taskRun, matchedIn))
		}
	}
	result.IsTruncated = result.TotalMatched > len(result.Matches)
	return result, nil
}

func (service Service) matchedFields(taskRun task.TaskRun, text string) []string {
	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return []string{}
	}
	matchedIn := []string{}
	for label, field := range map[string]string{"prompt": taskRun.Prompt, "result": taskRun.Result, "failureReason": taskRun.FailureReason} {
		if containsFold(field, trimmedText) {
			matchedIn = append(matchedIn, label)
		}
	}
	matchedIn = append(matchedIn, service.matchedEventNames(taskRun.TaskRunID, trimmedText)...)
	if len(matchedIn) == 0 {
		return nil
	}
	return sortedUnique(matchedIn)
}

func (service Service) matchedEventNames(taskRunID string, text string) []string {
	eventNames := []string{}
	for _, taskEvent := range service.store.ListTaskEvent(taskRunID) {
		if containsFold(taskEvent.Body, text) {
			eventNames = append(eventNames, strings.TrimSpace(taskEvent.Name))
		}
	}
	return eventNames
}

func matchFor(taskRun task.TaskRun, matchedIn []string) Match {
	return Match{
		TaskRunID:      taskRun.TaskRunID,
		ConversationID: taskRun.OriginConversationID,
		Status:         string(taskRun.Status),
		Prompt:         taskRun.Prompt,
		CreatedAt:      taskRun.CreatedAt,
		MatchedIn:      matchedIn,
	}
}

func matchesConversation(taskRun task.TaskRun, conversationID string) bool {
	trimmedConversationID := strings.TrimSpace(conversationID)
	if trimmedConversationID == "" {
		return true
	}
	return taskRun.OriginConversationID == trimmedConversationID
}

func boundedLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maximumLimit {
		return maximumLimit
	}
	return limit
}

func containsFold(haystack string, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	unique := []string{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}
