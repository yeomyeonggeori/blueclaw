package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/access"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

const (
	recordRecallLimit      = 3
	recordRecallSourceKind = "record"
)

type recordRecall struct {
	Facts    []memory.MemoryFact
	Failures []string
}

type recallSourceInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type recallSourceResult struct {
	Facts []recallSourceFact `json:"facts"`
}

type recallSourceFact struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	ValidAt string `json:"validAt"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) recallFromTheRecord(ctx context.Context, request ToolCatalogRequest, query string) recordRecall {
	recall := recordRecall{}
	if request.RecordCatalog == nil || strings.TrimSpace(request.RequesterEmail) == "" {
		return recall
	}
	for _, descriptor := range toolCatalogBuilder.capabilityToolDefinitions() {
		if !isRecallSource(descriptor) || !requesterMayCall(request, descriptor) {
			continue
		}
		facts, errorValue := recallFromSource(ctx, request, modelNameOf(descriptor), query)
		if errorValue != nil {
			recall.Failures = append(recall.Failures, modelNameOf(descriptor)+": "+errorValue.Error())
			continue
		}
		recall.Facts = append(recall.Facts, facts...)
	}
	return recall
}

func isRecallSource(descriptor capability.ToolDescriptor) bool {
	return descriptor.RecallSource && strings.TrimSpace(descriptor.AnsweredBy) == capability.AnsweredByRecord
}

func requesterMayCall(request ToolCatalogRequest, descriptor capability.ToolDescriptor) bool {
	return access.CanAccess(access.Request{PersonAccess: request.PersonAccess, Action: access.ActionExecute, Resource: descriptor.PolicyResource})
}

func recallFromSource(ctx context.Context, request ToolCatalogRequest, toolName string, query string) ([]memory.MemoryFact, error) {
	input, errorValue := json.Marshal(recallSourceInput{Query: query, Limit: recordRecallLimit})
	if errorValue != nil {
		return nil, errorValue
	}
	result, errorValue := request.RecordCatalog.CallTool(ctx, request.RequesterEmail, toolName, input)
	if errorValue != nil {
		return nil, errorValue
	}
	if result.IsError {
		return nil, errors.New("refused: " + string(resultInsideTheEnvelope(result.StructuredContent)))
	}
	return recalledFacts(result)
}

func recalledFacts(result mcp.ToolResult) ([]memory.MemoryFact, error) {
	var answered recallSourceResult
	if errorValue := json.Unmarshal(resultInsideTheEnvelope(result.StructuredContent), &answered); errorValue != nil {
		return nil, errors.New("the answer is not the recall contract: " + errorValue.Error())
	}
	facts := make([]memory.MemoryFact, 0, len(answered.Facts))
	for _, fact := range answered.Facts {
		content := strings.TrimSpace(fact.Content)
		if content == "" {
			continue
		}
		facts = append(facts, memory.MemoryFact{
			FactID:     strings.TrimSpace(fact.ID),
			Content:    content,
			SourceKind: recordRecallSourceKind,
			ValidAt:    dayOf(fact.ValidAt),
		})
	}
	return facts, nil
}

func dayOf(value string) time.Time {
	day, errorValue := time.Parse(time.DateOnly, strings.TrimSpace(value))
	if errorValue != nil {
		return time.Time{}
	}
	return day
}
