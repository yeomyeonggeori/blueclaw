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
	recordCandidateLimit   = 10
)

type recallReach float64

const (
	recallReachAtLaunch recallReach = 0.10
	recallReachOnSearch recallReach = 0.05
)

type recordRecall struct {
	Facts    []memory.MemoryFact
	Failures []string
}

type recallSourceInput struct {
	Query          string   `json:"query,omitempty"`
	Candidates     []string `json:"candidates,omitempty"`
	ChangedAfter   string   `json:"changedAfter,omitempty"`
	ChangedAfterID string   `json:"changedAfterID,omitempty"`
	Limit          int      `json:"limit"`
}

type recallSourceResult struct {
	Facts []recallSourceFact `json:"facts"`
	Frame string             `json:"frame"`
}

type recallSourceFact struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	ValidAt   string `json:"validAt"`
	ChangedAt string `json:"changedAt"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) recallFromTheRecord(ctx context.Context, request ToolCatalogRequest, query string, reach recallReach) recordRecall {
	recall := recordRecall{}
	if request.RecordCatalog == nil || strings.TrimSpace(request.RequesterEmail) == "" {
		return recall
	}
	for _, descriptor := range toolCatalogBuilder.capabilityToolDefinitions() {
		if !isRecallSource(descriptor) || !requesterMayCall(request, descriptor) {
			continue
		}
		source := modelNameOf(descriptor)
		if failure := toolCatalogBuilder.refreshRecordIndex(request, source); failure != "" {
			recall.Failures = append(recall.Failures, source+" index: "+failure)
		}
		candidates, errorValue := toolCatalogBuilder.recordCandidates(ctx, source, query, reach)
		if errorValue != nil {
			recall.Failures = append(recall.Failures, source+" index: "+errorValue.Error())
		}
		answered, errorValue := askRecallSource(ctx, request, source, recallSourceInput{Query: query, Candidates: candidates, Limit: recordRecallLimit})
		if errorValue != nil {
			recall.Failures = append(recall.Failures, source+": "+errorValue.Error())
			continue
		}
		recall.Facts = append(recall.Facts, recalledFacts(answered)...)
	}
	return recall
}

func (toolCatalogBuilder *ToolCatalogBuilder) recordCandidates(ctx context.Context, source string, query string, reach recallReach) ([]string, error) {
	if toolCatalogBuilder.memoryStores == nil {
		return nil, nil
	}
	rises, errorValue := toolCatalogBuilder.memoryStores.RecordIndex().Nearest(ctx, source, query, float64(reach), recordCandidateLimit)
	if errorValue != nil {
		return nil, errorValue
	}
	candidates := make([]string, 0, len(rises))
	for _, rise := range rises {
		candidates = append(candidates, rise.ID)
	}
	return candidates, nil
}

func isRecallSource(descriptor capability.ToolDescriptor) bool {
	return descriptor.RecallSource && strings.TrimSpace(descriptor.AnsweredBy) == capability.AnsweredByRecord
}

func requesterMayCall(request ToolCatalogRequest, descriptor capability.ToolDescriptor) bool {
	return access.CanAccess(access.Request{PersonAccess: request.PersonAccess, Action: access.ActionExecute, Resource: descriptor.PolicyResource})
}

func askRecallSource(ctx context.Context, request ToolCatalogRequest, toolName string, asked recallSourceInput) (recallSourceResult, error) {
	input, errorValue := json.Marshal(asked)
	if errorValue != nil {
		return recallSourceResult{}, errorValue
	}
	result, errorValue := request.RecordCatalog.CallTool(ctx, request.RequesterEmail, toolName, input)
	if errorValue != nil {
		return recallSourceResult{}, errorValue
	}
	if result.IsError {
		return recallSourceResult{}, errors.New("refused: " + string(resultInsideTheEnvelope(result.StructuredContent)))
	}
	return recallContractOf(result)
}

func recallContractOf(result mcp.ToolResult) (recallSourceResult, error) {
	var answered recallSourceResult
	if errorValue := json.Unmarshal(resultInsideTheEnvelope(result.StructuredContent), &answered); errorValue != nil {
		return recallSourceResult{}, errors.New("the answer is not the recall contract: " + errorValue.Error())
	}
	return answered, nil
}

func recalledFacts(answered recallSourceResult) []memory.MemoryFact {
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
	return facts
}

func dayOf(value string) time.Time {
	day, errorValue := time.Parse(time.DateOnly, strings.TrimSpace(value))
	if errorValue != nil {
		return time.Time{}
	}
	return day
}
