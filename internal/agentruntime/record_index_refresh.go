package agentruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

const (
	recordIndexRefreshInterval = 5 * time.Minute
	recordIndexRefreshTimeout  = 10 * time.Minute
	recordIndexListingPage     = 100
	recordIndexEarliestChange  = "1970-01-01T00:00:00Z"
)

type recordIndexCursor struct {
	ChangedAfter   string
	ChangedAfterID string
}

type recordIndexRefresh struct {
	cursor    recordIndexCursor
	startedAt time.Time
	isRunning bool
	failure   string
}

type recordIndexRefreshes struct {
	mutex       sync.Mutex
	byRequester map[string]*recordIndexRefresh
}

func (toolCatalogBuilder *ToolCatalogBuilder) refreshRecordIndex(request ToolCatalogRequest, source string) string {
	if toolCatalogBuilder.memoryStores == nil {
		return ""
	}
	key := source + "\x00" + strings.ToLower(strings.TrimSpace(request.RequesterEmail))
	cursor, isDue, failure := toolCatalogBuilder.recordIndexRefreshes.begin(key, time.Now())
	if !isDue {
		return failure
	}
	index := toolCatalogBuilder.memoryStores.RecordIndex()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), recordIndexRefreshTimeout)
		defer cancel()
		reached, errorValue := listIntoRecordIndex(ctx, request, index, source, cursor)
		toolCatalogBuilder.recordIndexRefreshes.finish(key, reached, errorValue)
	}()
	return failure
}

func listIntoRecordIndex(ctx context.Context, request ToolCatalogRequest, index *memory.RecordIndex, source string, cursor recordIndexCursor) (recordIndexCursor, error) {
	for {
		listedFrom := cursor
		answered, errorValue := askRecallSource(ctx, request, source, recallSourceInput{
			ChangedAfter:   firstNonEmptyString(cursor.ChangedAfter, recordIndexEarliestChange),
			ChangedAfterID: cursor.ChangedAfterID,
			Limit:          recordIndexListingPage,
		})
		if errorValue != nil {
			return cursor, errorValue
		}
		if frame := strings.TrimSpace(answered.Frame); frame != "" {
			if errorValue := index.HoldFrame(ctx, source, frame); errorValue != nil {
				return cursor, errorValue
			}
		}
		for _, fact := range answered.Facts {
			if strings.TrimSpace(fact.ChangedAt) == "" {
				return cursor, errors.New("a listing answered " + fact.ID + " without when it changed")
			}
			entry := memory.RecordEntry{ID: strings.TrimSpace(fact.ID), Content: strings.TrimSpace(fact.Content), ChangedAt: fact.ChangedAt}
			if errorValue := index.Hold(ctx, source, entry); errorValue != nil {
				return cursor, errorValue
			}
			cursor = recordIndexCursor{ChangedAfter: fact.ChangedAt, ChangedAfterID: entry.ID}
		}
		if len(answered.Facts) == 0 {
			return cursor, nil
		}
		if cursor == listedFrom {
			return cursor, errors.New("a listing answered nothing after " + cursor.ChangedAfter + " " + cursor.ChangedAfterID)
		}
	}
}

func (refreshes *recordIndexRefreshes) begin(key string, now time.Time) (recordIndexCursor, bool, string) {
	refreshes.mutex.Lock()
	defer refreshes.mutex.Unlock()
	if refreshes.byRequester == nil {
		refreshes.byRequester = map[string]*recordIndexRefresh{}
	}
	refresh, isKnown := refreshes.byRequester[key]
	if !isKnown {
		refresh = &recordIndexRefresh{}
		refreshes.byRequester[key] = refresh
	}
	failure := refresh.failure
	refresh.failure = ""
	if refresh.isRunning || (!refresh.startedAt.IsZero() && now.Sub(refresh.startedAt) < recordIndexRefreshInterval) {
		return recordIndexCursor{}, false, failure
	}
	refresh.isRunning = true
	refresh.startedAt = now
	return refresh.cursor, true, failure
}

func (refreshes *recordIndexRefreshes) finish(key string, reached recordIndexCursor, errorValue error) {
	refreshes.mutex.Lock()
	defer refreshes.mutex.Unlock()
	refresh := refreshes.byRequester[key]
	refresh.isRunning = false
	refresh.cursor = reached
	if errorValue != nil {
		refresh.failure = errorValue.Error()
	}
}
