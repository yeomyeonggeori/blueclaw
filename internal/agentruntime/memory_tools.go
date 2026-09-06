package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type memorySearchToolInput struct {
	Query string `json:"query"`
}

type memoryRememberToolInput struct {
	Content string `json:"content"`
}

type memoryFactMutationInput struct {
	FactID      string `json:"factID"`
	NamespaceID string `json:"namespaceID"`
	Content     string `json:"content,omitempty"`
}

type memorySearchStatus string

const (
	memorySearchComplete memorySearchStatus = "complete"
)

type memorySearchSource string

const (
	memorySearchGraphSource memorySearchSource = "graph_memory"
)

type memorySearchFact struct {
	FactID      string    `json:"factID"`
	ScopeType   string    `json:"scopeType"`
	NamespaceID string    `json:"namespaceID"`
	Content     string    `json:"content"`
	SourceKind  string    `json:"sourceKind"`
	ValidAt     time.Time `json:"validAt"`
	Score       *float64  `json:"score,omitempty"`
}

type memorySearchToolOutput struct {
	Facts        []memorySearchFact   `json:"facts"`
	SearchStatus memorySearchStatus   `json:"searchStatus"`
	Sources      []memorySearchSource `json:"sources"`
}

var (
	memorySearchInputSchema         = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"pattern":"\\S"}},"required":["query"],"additionalProperties":false}`)
	memorySearchOutputSchema        = json.RawMessage(`{"type":"object","properties":{"facts":{"type":"array","items":{"type":"object","properties":{"factID":{"type":"string"},"scopeType":{"type":"string"},"namespaceID":{"type":"string"},"content":{"type":"string"},"sourceKind":{"type":"string"},"validAt":{"type":"string","format":"date-time"},"score":{"type":"number"}},"required":["factID","scopeType","namespaceID","content","sourceKind","validAt"],"additionalProperties":false}},"searchStatus":{"type":"string","enum":["complete"]},"sources":{"type":"array","items":{"const":"graph_memory"},"minItems":1,"maxItems":1}},"required":["facts","searchStatus","sources"],"additionalProperties":false}`)
	memoryRememberInputSchema       = json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","minLength":1,"maxLength":600,"pattern":"\\S"}},"required":["content"],"additionalProperties":false}`)
	memoryRememberInputIntentSchema = json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","minLength":1,"maxLength":600,"pattern":"\\S"}},"additionalProperties":false}`)
	memoryFactUpdateInputSchema     = json.RawMessage(`{"type":"object","properties":{"factID":{"type":"string","minLength":1,"pattern":"\\S"},"namespaceID":{"type":"string","minLength":1,"pattern":"\\S"},"content":{"type":"string","minLength":1,"maxLength":600,"pattern":"\\S"}},"required":["factID","namespaceID","content"],"additionalProperties":false}`)
	memoryFactDeleteInputSchema     = json.RawMessage(`{"type":"object","properties":{"factID":{"type":"string","minLength":1,"pattern":"\\S"},"namespaceID":{"type":"string","minLength":1,"pattern":"\\S"}},"required":["factID","namespaceID"],"additionalProperties":false}`)
	memoryFactUpdateIntentSchema    = json.RawMessage(`{"type":"object","properties":{"factID":{"type":"string"},"namespaceID":{"type":"string"},"content":{"type":"string"}},"additionalProperties":false}`)
	memoryFactDeleteIntentSchema    = json.RawMessage(`{"type":"object","properties":{"factID":{"type":"string"},"namespaceID":{"type":"string"}},"additionalProperties":false}`)
	memoryFactUpdateOutputSchema    = json.RawMessage(`{"type":"object","properties":{"factID":{"type":"string"},"namespaceID":{"type":"string"},"content":{"type":"string"}},"required":["factID","namespaceID","content"],"additionalProperties":false}`)
	memoryFactDeleteOutputSchema    = json.RawMessage(`{"type":"object","properties":{"factID":{"type":"string"},"namespaceID":{"type":"string"},"deleted":{"type":"boolean"}},"required":["factID","namespaceID","deleted"],"additionalProperties":false}`)
	memoryRememberOutputSchema      = json.RawMessage(`{"type":"object","properties":{"accepted":{"type":"boolean"},"jobID":{"type":"string","pattern":"\\S"},"status":{"type":"string","enum":["persisted","queued_volatile","failed"]},"durability":{"type":"string","enum":["durable","volatile","none"]},"graphitiStatus":{"type":"string"},"markdownUpdated":{"type":"boolean"},"failureCode":{"type":"string"},"failureComponent":{"type":"string"}},"required":["accepted","jobID","status","durability"],"additionalProperties":false}`)
)

func registerMemoryTools(toolCatalogBuilder *ToolCatalogBuilder, toolRegistry *toolcontract.ToolSet, request ToolCatalogRequest) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[memorySearchToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "memory_search",
			Description: "Search memory only when the user's request needs remembered facts, preferences, relationships, or prior decisions. Search by meaning within memory allowed for this requester and conversation; do not search for every request or use it as a substitute for the current profile.",
			InputSchema: memorySearchInputSchema,
		},
		Handler: func(toolContext context.Context, input memorySearchToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.searchMemoryTool(toolContext, input, request)
		},
		Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[memoryRememberToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "memory_remember",
			Description: "Store one concise durable fact, preference, relationship, or decision for the current person or active circle; nothing is remembered unless this tool is called. Save only information likely to help in future work. Keep content a single compact standalone fact. Do not copy user.json, dump conversation transcripts, or store secrets, one-off requests, transient task details, or small talk. This is private assistant recall and is never a visible conversation message.",
			InputSchema: memoryRememberInputSchema,
		},
		Handler: func(toolContext context.Context, input memoryRememberToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.rememberMemoryTool(toolContext, input, request)
		},
		Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[memoryFactMutationInput, toolcontract.ToolResult]{Definition: toolcontract.ToolDefinition{Name: "memory_update", Description: "Update one memory fact returned by memory_search using its exact factID and namespaceID.", InputSchema: memoryFactUpdateInputSchema}, Handler: func(toolContext context.Context, input memoryFactMutationInput) (toolcontract.ToolResult, error) {
		return toolCatalogBuilder.updateMemoryTool(toolContext, input, request)
	}, Result: toolcontract.IdentityToolResult})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[memoryFactMutationInput, toolcontract.ToolResult]{Definition: toolcontract.ToolDefinition{Name: "memory_delete", Description: "Delete one memory fact returned by memory_search using its exact factID and namespaceID.", InputSchema: memoryFactDeleteInputSchema}, Handler: func(toolContext context.Context, input memoryFactMutationInput) (toolcontract.ToolResult, error) {
		return toolCatalogBuilder.deleteMemoryTool(toolContext, input, request)
	}, Result: toolcontract.IdentityToolResult})
}

func (toolCatalogBuilder *ToolCatalogBuilder) updateMemoryTool(ctx context.Context, input memoryFactMutationInput, request ToolCatalogRequest) (toolcontract.ToolResult, error) {
	if request.ActiveCircleConflict || !memoryNamespaceIsAllowed(request, input.NamespaceID) {
		return toolcontract.ToolFailureResult(toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "memory_update", "memory fact is not accessible"), nil
	}
	if toolCatalogBuilder.memoryService == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "memory_update", "memory service is unavailable"), nil
	}
	updatedFact, errorValue := toolCatalogBuilder.memoryService.UpdateFact(ctx, memory.MemoryFactUpdateRequest{FactID: input.FactID, NamespaceID: input.NamespaceID, Content: input.Content})
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "memory_update", errorValue.Error()), nil
	}
	document := json.RawMessage(marshalToolResult(map[string]string{"factID": updatedFact.FactID, "namespaceID": updatedFact.NamespaceID, "content": updatedFact.Content}))
	return toolcontract.ToolSuccessData(string(document), document), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) deleteMemoryTool(ctx context.Context, input memoryFactMutationInput, request ToolCatalogRequest) (toolcontract.ToolResult, error) {
	if request.ActiveCircleConflict || !memoryNamespaceIsAllowed(request, input.NamespaceID) {
		return toolcontract.ToolFailureResult(toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "memory_delete", "memory fact is not accessible"), nil
	}
	if toolCatalogBuilder.memoryService == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "memory_delete", "memory service is unavailable"), nil
	}
	result, errorValue := toolCatalogBuilder.memoryService.DeleteFact(ctx, memory.MemoryFactDeleteRequest{FactID: input.FactID, NamespaceID: input.NamespaceID})
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "memory_delete", errorValue.Error()), nil
	}
	document := json.RawMessage(marshalToolResult(result))
	return toolcontract.ToolSuccessData(string(document), document), nil
}

func memoryNamespaceIsAllowed(request ToolCatalogRequest, namespaceID string) bool {
	for _, namespace := range searchMemoryNamespaces(request) {
		if namespace.NamespaceID == strings.TrimSpace(namespaceID) {
			return true
		}
	}
	return false
}

func (toolCatalogBuilder *ToolCatalogBuilder) searchMemoryTool(toolContext context.Context, input memorySearchToolInput, request ToolCatalogRequest) (toolcontract.ToolResult, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "memory_search", "memory_search query is required"), nil
	}
	if request.ActiveCircleConflict {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.Conflict, "memory_search", "memory_search has multiple active circle candidates"), nil
	}
	memoryRequest := TaskMemoryRequest{
		Query:                     query,
		RequesterPersonID:         request.RequesterPersonID,
		ConversationID:            request.ConversationID,
		PersonAccess:              request.PersonAccess,
		MemoryNamespaces:          searchMemoryNamespaces(request),
		AccessibleConversationIDs: request.AccessibleConversationIDs,
	}
	if !toolCatalogBuilder.canSearchGraphMemory() {
		return memorySearchUnavailableResult(), nil
	}
	memoryFacts, errorValue := toolCatalogBuilder.SearchMemory(toolContext, memoryRequest)
	if errorValue != nil {
		return memorySearchUnavailableResult(), nil
	}
	return memorySearchSuccess(memoryFacts, memorySearchComplete, []memorySearchSource{memorySearchGraphSource}), nil
}

func memorySearchUnavailableResult() toolcontract.ToolResult {
	message := "Persistent memory search is unavailable."
	return toolcontract.ToolResult{
		Output: toolcontract.ToolOutput{Content: message},
		Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureDependencyUnavailable,
			Code:            toolcontract.FailureCodes.Unavailable.String(),
			Stage:           "graphiti_search",
			UserSafeSummary: message,
			Retryable:       true,
			SafeRetry:       false,
		},
	}
}

func memorySearchSuccess(memoryFacts []memory.MemoryFact, searchStatus memorySearchStatus, sources []memorySearchSource) toolcontract.ToolResult {
	output := memorySearchToolOutput{
		Facts:        projectMemorySearchFacts(memoryFacts),
		SearchStatus: searchStatus,
		Sources:      append([]memorySearchSource{}, sources...),
	}
	document := json.RawMessage(marshalToolResult(output))
	return toolcontract.ToolSuccessData(string(document), document)
}

func projectMemorySearchFacts(memoryFacts []memory.MemoryFact) []memorySearchFact {
	projectedFacts := make([]memorySearchFact, 0, len(memoryFacts))
	for _, memoryFact := range memoryFacts {
		projectedFact := memorySearchFact{
			FactID:      memoryFact.FactID,
			ScopeType:   memoryFact.ScopeType,
			NamespaceID: memoryFact.NamespaceID,
			Content:     memoryFact.Content,
			SourceKind:  memoryFact.SourceKind,
			ValidAt:     memoryFact.ValidAt,
		}
		if memoryFact.Score != 0 {
			projectedFact.Score = &memoryFact.Score
		}
		projectedFacts = append(projectedFacts, projectedFact)
	}
	return projectedFacts
}

func (toolCatalogBuilder *ToolCatalogBuilder) canSearchGraphMemory() bool {
	return toolCatalogBuilder.memoryService != nil && toolCatalogBuilder.memoryService.HasGraphStore()
}

func (toolCatalogBuilder *ToolCatalogBuilder) SearchMemory(ctx context.Context, request TaskMemoryRequest) ([]memory.MemoryFact, error) {
	if toolCatalogBuilder.memoryService == nil {
		return nil, nil
	}
	return toolCatalogBuilder.memoryService.SearchMemory(ctx, memorySearchRequest(request))
}

func memorySearchRequest(request TaskMemoryRequest) memory.MemorySearchRequest {
	return memory.MemorySearchRequest{
		Query:                     request.Query,
		ReaderPersonID:            request.RequesterPersonID,
		ReaderCircles:             request.PersonAccess.Circles,
		ResourceAccessRules:       request.PersonAccess.ResourceAccessRules,
		ReaderSecurityLevelRank:   request.PersonAccess.SecurityLevelRank,
		ReaderGrantedClasses:      request.PersonAccess.GrantedClasses,
		ConversationID:            request.ConversationID,
		AccessibleConversationIDs: request.AccessibleConversationIDs,
		Namespaces:                request.MemoryNamespaces,
		ExplicitNamespacesOnly:    true,
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) rememberMemoryTool(toolContext context.Context, input memoryRememberToolInput, request ToolCatalogRequest) (toolcontract.ToolResult, error) {
	content := strings.TrimSpace(input.Content)
	if gateMessage := memory.RememberContentGateMessage(content); gateMessage != "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "memory_remember", gateMessage), nil
	}
	if request.ActiveCircleConflict {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.Conflict, "memory_remember", "memory_remember has multiple active circle candidates"), nil
	}
	namespace, errorMessage := resolveRememberMemoryNamespace(request)
	if errorMessage != "" {
		return toolcontract.ToolFailureResult(toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "memory_remember", errorMessage), nil
	}
	job := memory.PrepareMemoryUpdateJob(memory.MemoryUpdateJob{
		Namespace:       namespace,
		Content:         content,
		Platform:        request.Platform,
		ConversationID:  request.ConversationID,
		SenderPersonID:  request.RequesterPersonID,
		SourceReference: firstNonEmptyString(request.ReplyTargetID, request.ConversationID),
		OccurredAt:      time.Now().UTC(),
	})
	return toolCatalogBuilder.persistMemoryUpdateTool(toolContext, job), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) persistMemoryUpdateTool(ctx context.Context, job memory.MemoryUpdateJob) toolcontract.ToolResult {
	if toolCatalogBuilder.memoryService == nil || !toolCatalogBuilder.memoryService.HasGraphStore() {
		return memoryRememberResult(failedMemoryUpdate(job.JobID, "memory_unavailable", "graph_memory"))
	}
	_, errorValue := toolCatalogBuilder.memoryService.AddEpisode(ctx, memory.MemoryEpisode{
		EpisodeID: job.JobID, Platform: job.Platform, MessageID: job.JobID,
		ConversationID: job.ConversationID, SenderPersonID: job.SenderPersonID,
		Prompt: job.Content, OccurredAt: job.OccurredAt,
		Namespaces: []memory.MemoryNamespace{job.Namespace}, Source: "memory_remember", SourceReference: job.SourceReference,
	})
	if errorValue != nil {
		return memoryRememberResult(failedMemoryUpdate(job.JobID, "memory_persist_failed", "graph_memory"))
	}
	return memoryRememberResult(memory.MemoryUpdateAccepted{Accepted: true, JobID: job.JobID, Status: "persisted", Durability: "durable", GraphitiStatus: "persisted"})
}

func memoryRememberResult(accepted memory.MemoryUpdateAccepted) toolcontract.ToolResult {
	document := json.RawMessage(marshalToolResult(accepted))
	if !accepted.Accepted {
		return toolcontract.ToolFailureData(
			toolcontract.FailureExternalService,
			toolcontract.FailureCodes.OperationFailed,
			firstNonEmptyString(accepted.FailureComponent, "memory_remember"),
			"memory update was not accepted",
			document,
		)
	}
	return toolcontract.ToolSuccessData(string(document), document)
}

func failedMemoryUpdate(jobID string, failureCode string, failureComponent string) memory.MemoryUpdateAccepted {
	return memory.MemoryUpdateAccepted{
		Accepted:         false,
		JobID:            jobID,
		Status:           "failed",
		Durability:       "none",
		GraphitiStatus:   "not_queued",
		FailureCode:      failureCode,
		FailureComponent: failureComponent,
	}
}

func resolveRememberMemoryNamespace(request ToolCatalogRequest) (memory.MemoryNamespace, string) {
	if strings.TrimSpace(request.ActiveCircleID) == "" {
		return resolvePersonMemoryNamespace(request)
	}
	return resolveCircleMemoryNamespace(request.ActiveCircleID, request)
}

func resolvePersonMemoryNamespace(request ToolCatalogRequest) (memory.MemoryNamespace, string) {
	if strings.TrimSpace(request.RequesterPersonID) == "" {
		return memory.MemoryNamespace{}, "memory_remember person scope requires requester person ID"
	}
	for _, namespace := range request.MemoryNamespaces {
		if namespace.ScopeType == memory.ScopeTypeUser && namespace.ScopePersonID == request.RequesterPersonID {
			return namespace, ""
		}
	}
	return memory.UserNamespace(request.RequesterPersonID), ""
}

func resolveCircleMemoryNamespace(circleID string, request ToolCatalogRequest) (memory.MemoryNamespace, string) {
	normalizedCircleID := strings.ToLower(strings.TrimSpace(circleID))
	if normalizedCircleID == "" {
		return memory.MemoryNamespace{}, "memory_remember circle memory requires active circle context"
	}
	if !personAccessIncludesCircle(request.PersonAccess, normalizedCircleID) {
		return memory.MemoryNamespace{}, "memory_remember circle memory is not accessible"
	}
	for _, namespace := range request.MemoryNamespaces {
		if namespace.ScopeType == memory.ScopeTypeCircle && namespace.ScopeCircleID == normalizedCircleID {
			return namespace, ""
		}
	}
	return memory.CircleNamespace(memory.DefaultWorkspaceID, normalizedCircleID), ""
}

func personAccessIncludesCircle(personAccess policy.PersonAccess, circleID string) bool {
	for _, accessibleCircleID := range personAccess.Circles {
		if strings.ToLower(strings.TrimSpace(accessibleCircleID)) == circleID {
			return true
		}
	}
	return false
}
