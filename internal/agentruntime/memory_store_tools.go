package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluememo"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

type memorySearchToolInput struct {
	Query string `json:"query"`
}

type memoryRememberToolInput struct {
	Content string `json:"content"`
}

type memorySearchStatus string

const (
	memorySearchComplete memorySearchStatus = "complete"
	memorySearchDegraded memorySearchStatus = "degraded"
)

type memorySearchFact struct {
	FactID     string    `json:"factID"`
	ScopeType  string    `json:"scopeType"`
	Content    string    `json:"content"`
	SourceKind string    `json:"sourceKind"`
	ValidAt    time.Time `json:"validAt"`
	Score      *float64  `json:"score,omitempty"`
}

type memorySearchToolOutput struct {
	Facts        []memorySearchFact `json:"facts"`
	SearchStatus memorySearchStatus `json:"searchStatus"`
}

var (
	memorySearchInputSchema         = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"pattern":"\\S"}},"required":["query"],"additionalProperties":false}`)
	memorySearchOutputSchema        = json.RawMessage(`{"type":"object","properties":{"facts":{"type":"array","items":{"type":"object","properties":{"factID":{"type":"string"},"scopeType":{"type":"string","enum":["private","circle","workspace"]},"content":{"type":"string"},"sourceKind":{"type":"string","enum":["identity","preference","fact","episode","temporary"]},"validAt":{"type":"string","format":"date-time"},"score":{"type":"number"}},"required":["factID","scopeType","content","sourceKind","validAt"],"additionalProperties":false}},"searchStatus":{"type":"string","enum":["complete","degraded"]}},"required":["facts","searchStatus"],"additionalProperties":false}`)
	memoryRememberInputSchema       = json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","minLength":1,"maxLength":600,"pattern":"\\S"}},"required":["content"],"additionalProperties":false}`)
	memoryRememberInputIntentSchema = json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","minLength":1,"maxLength":600,"pattern":"\\S"}},"additionalProperties":false}`)
	memoryRememberOutputSchema      = json.RawMessage(`{"type":"object","properties":{"accepted":{"type":"boolean"},"episodeID":{"type":"string","pattern":"\\S"},"factIDs":{"type":"array","items":{"type":"string"}},"supersededFactIDs":{"type":"array","items":{"type":"string"}},"reinforcedFactIDs":{"type":"array","items":{"type":"string"}},"failureCode":{"type":"string"}},"required":["accepted","episodeID","factIDs","supersededFactIDs","reinforcedFactIDs"],"additionalProperties":false}`)
)

type memoryForgetToolInput struct {
	FactIDs []string `json:"factIDs"`
	Reason  string   `json:"reason"`
}

type memoryForgetToolOutput struct {
	ForgottenFactIDs []string `json:"forgottenFactIDs"`
	Reason           string   `json:"reason"`
}

type memoryStoreRememberOutput struct {
	Accepted          bool     `json:"accepted"`
	EpisodeID         string   `json:"episodeID"`
	FactIDs           []string `json:"factIDs"`
	SupersededFactIDs []string `json:"supersededFactIDs"`
	ReinforcedFactIDs []string `json:"reinforcedFactIDs"`
	FailureCode       string   `json:"failureCode,omitempty"`
}

var (
	memoryForgetInputSchema       = json.RawMessage(`{"type":"object","properties":{"factIDs":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20},"reason":{"type":"string"}},"required":["factIDs","reason"],"additionalProperties":false}`)
	memoryForgetInputIntentSchema = json.RawMessage(`{"type":"object","properties":{"factIDs":{"type":"array","items":{"type":"string"}},"reason":{"type":"string"}},"additionalProperties":false}`)
	memoryForgetOutputSchema      = json.RawMessage(`{"type":"object","properties":{"forgottenFactIDs":{"type":"array","items":{"type":"string"},"minItems":1,"uniqueItems":true},"reason":{"type":"string"}},"required":["forgottenFactIDs","reason"],"additionalProperties":false}`)
)

type surfacedFactIDs struct {
	mutex sync.Mutex
	ids   map[string]bool
}

func (surfaced *surfacedFactIDs) add(factIDs []string) {
	surfaced.mutex.Lock()
	defer surfaced.mutex.Unlock()
	if surfaced.ids == nil {
		surfaced.ids = map[string]bool{}
	}
	for _, factID := range factIDs {
		surfaced.ids[factID] = true
	}
}

func (surfaced *surfacedFactIDs) unknown(factIDs []string) []string {
	surfaced.mutex.Lock()
	defer surfaced.mutex.Unlock()
	unknownFactIDs := []string{}
	for _, factID := range factIDs {
		if !surfaced.ids[factID] {
			unknownFactIDs = append(unknownFactIDs, factID)
		}
	}
	return unknownFactIDs
}

func (surfaced *surfacedFactIDs) known() []string {
	surfaced.mutex.Lock()
	defer surfaced.mutex.Unlock()
	known := make([]string, 0, len(surfaced.ids))
	for factID := range surfaced.ids {
		known = append(known, factID)
	}
	sort.Strings(known)
	return known
}

func registerStoreMemoryTools(toolCatalogBuilder *ToolCatalogBuilder, toolRegistry *toolcontract.ToolSet, request ToolCatalogRequest) {
	surfaced := &surfacedFactIDs{}
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[memorySearchToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "memory_search",
			Description: "Search what the assistant remembers about people, their preferences, and their work, within what this requester may read. Returns facts by meaning with their IDs; only IDs returned here can be passed to memory_forget.",
			InputSchema: memorySearchInputSchema,
		},
		Handler: func(toolContext context.Context, input memorySearchToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.searchStoreMemoryTool(toolContext, input, request, surfaced), nil
		},
		Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[memoryRememberToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "memory_remember",
			Description: "Remember something a person told you or asked you to keep: a preference, a fact about them or their work, a change to something already remembered. Write one plain sentence naming the person. If the memory already holds a version of it, the store updates that version; you never need to look it up first. This is the assistant's private recall, never a message anyone sees. Do not store secrets, one-off requests, or small talk.",
			InputSchema: memoryRememberInputSchema,
		},
		Handler: func(toolContext context.Context, input memoryRememberToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.rememberStoreMemoryTool(toolContext, input, request), nil
		},
		Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[memoryForgetToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "memory_forget",
			Description: "Forget remembered facts a person asked you to drop. Pass fact IDs exactly as memory_search returned them in this task, and say why in the person's words. Forgetting is not undone.",
			InputSchema: memoryForgetInputSchema,
		},
		Handler: func(toolContext context.Context, input memoryForgetToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.forgetStoreMemoryTool(toolContext, input, request, surfaced), nil
		},
		Result: toolcontract.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) searchStoreMemoryTool(ctx context.Context, input memorySearchToolInput, request ToolCatalogRequest, surfaced *surfacedFactIDs) toolcontract.ToolResult {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "memory_search", "memory_search query is required")
	}
	searchResult, errorValue := toolCatalogBuilder.memoryStore.Search(ctx, toolCatalogBuilder.memoryReader(request.PersonAccess), query, bluememo.DefaultSearchResultLimit)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "memory_search", "memory search failed: "+errorValue.Error())
	}
	facts := make([]memorySearchFact, 0, len(searchResult.Facts))
	factIDs := make([]string, 0, len(searchResult.Facts))
	for _, scoredFact := range searchResult.Facts {
		facts = append(facts, projectStoreMemoryFact(scoredFact))
		factIDs = append(factIDs, scoredFact.Fact.FactID)
	}
	surfaced.add(factIDs)
	status := memorySearchComplete
	if searchResult.Mode != bluememo.SearchModeHybrid {
		status = memorySearchDegraded
	}
	output := memorySearchToolOutput{Facts: facts, SearchStatus: status}
	document := json.RawMessage(marshalToolResult(output))
	return toolcontract.ToolSuccessData(string(document), document)
}

func projectStoreMemoryFact(scoredFact bluememo.ScoredFact) memorySearchFact {
	projected := memorySearchFact{
		FactID:     scoredFact.Fact.FactID,
		ScopeType:  scoredFact.Fact.ScopeType,
		Content:    scoredFact.Fact.Content,
		SourceKind: scoredFact.Fact.Kind,
		ValidAt:    scoredFact.Fact.ValidFrom,
	}
	if scoredFact.Score != 0 {
		score := scoredFact.Score
		projected.Score = &score
	}
	return projected
}

func (toolCatalogBuilder *ToolCatalogBuilder) rememberStoreMemoryTool(ctx context.Context, input memoryRememberToolInput, request ToolCatalogRequest) toolcontract.ToolResult {
	content := strings.TrimSpace(input.Content)
	if gateMessage := memory.RememberContentGateMessage(content); gateMessage != "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "memory_remember", gateMessage)
	}
	if strings.TrimSpace(request.RequesterPersonID) == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "memory_remember", "memory_remember requires a requester")
	}
	if request.ActiveCircleConflict {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.Conflict, "memory_remember", "memory_remember has multiple active circle candidates")
	}
	if toolCatalogBuilder.memoryIngester == nil {
		return memoryStoreRememberFailure("ingester_unavailable", "memory ingestion is not configured")
	}
	now := time.Now().UTC()
	result, errorValue := toolCatalogBuilder.memoryIngester.Ingest(ctx, bluememo.IngestRequest{
		Episode: bluememo.Episode{
			EpisodeID:         bluememo.NewIdentifier(),
			SourceKind:        bluememo.EpisodeSourceKindExplicit,
			SourceID:          bluememo.NewIdentifier(),
			RequesterPersonID: request.RequesterPersonID,
			ConversationID:    request.ConversationID,
			Content:           content,
			OccurredAt:        now,
		},
		Reader:         toolCatalogBuilder.memoryReader(request.PersonAccess),
		RequesterName:  request.RequesterName,
		ActiveCircleID: strings.TrimSpace(request.ActiveCircleID),
		Label:          memorySecurityLabelForRequest(request),
	})
	if errorValue != nil {
		return memoryStoreRememberFailure("ingest_failed", errorValue.Error())
	}
	output := memoryStoreRememberOutput{
		Accepted:          true,
		EpisodeID:         result.EpisodeID,
		FactIDs:           factIDsOf(result.Facts),
		SupersededFactIDs: result.SupersededFactIDs,
		ReinforcedFactIDs: result.ReinforcedFactIDs,
	}
	document := json.RawMessage(marshalToolResult(output))
	return toolcontract.ToolSuccessData(string(document), document)
}

func memoryStoreRememberFailure(failureCode string, summary string) toolcontract.ToolResult {
	output := memoryStoreRememberOutput{
		Accepted:          false,
		EpisodeID:         "none",
		FactIDs:           []string{},
		SupersededFactIDs: []string{},
		ReinforcedFactIDs: []string{},
		FailureCode:       failureCode,
	}
	document := json.RawMessage(marshalToolResult(output))
	return toolcontract.ToolFailureData(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "memory_remember", summary, document)
}

func (toolCatalogBuilder *ToolCatalogBuilder) forgetStoreMemoryTool(ctx context.Context, input memoryForgetToolInput, request ToolCatalogRequest, surfaced *surfacedFactIDs) toolcontract.ToolResult {
	factIDs := trimNonEmptyStrings(input.FactIDs)
	if len(factIDs) == 0 {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "memory_forget", "memory_forget needs at least one fact ID")
	}
	if unknownFactIDs := surfaced.unknown(factIDs); len(unknownFactIDs) > 0 {
		return toolcontract.ToolFailureResult(
			toolcontract.FailureInvalidInput,
			toolcontract.FailureCodes.InvalidInput,
			"memory_forget",
			"memory_forget only accepts fact IDs memory_search returned in this task; unknown: "+strings.Join(unknownFactIDs, ", ")+"; known: "+strings.Join(surfaced.known(), ", "),
		)
	}
	forgottenFactIDs, errorValue := toolCatalogBuilder.memoryStore.Forget(ctx, toolCatalogBuilder.memoryReader(request.PersonAccess), factIDs, strings.TrimSpace(input.Reason))
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "memory_forget", "memory forget failed: "+errorValue.Error())
	}
	if len(forgottenFactIDs) == 0 {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.NotFound, "memory_forget", "none of the facts are live and readable any more")
	}
	output := memoryForgetToolOutput{ForgottenFactIDs: forgottenFactIDs, Reason: strings.TrimSpace(input.Reason)}
	document := json.RawMessage(marshalToolResult(output))
	return toolcontract.ToolSuccessData(string(document), document)
}

func memorySecurityLabelForRequest(request ToolCatalogRequest) bluememo.SecurityLabel {
	if request.MemoryLabel.RequiredClasses != nil {
		return request.MemoryLabel
	}
	return memory.LabelForAccess(request.PersonAccess)
}

func factIDsOf(facts []bluememo.Fact) []string {
	factIDs := make([]string, 0, len(facts))
	for _, fact := range facts {
		factIDs = append(factIDs, fact.FactID)
	}
	return factIDs
}
