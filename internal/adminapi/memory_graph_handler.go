package adminapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type MemoryGraphHandler struct {
	MemoryService  *memory.MemoryService
	Reporter       memory.GraphMemoryReporter
	Migrator       memory.GraphMemoryMigrator
	MarkdownStore  *memory.MarkdownStore
	Identity       *identity.IdentityService
	ReaderPersonID func(*http.Request) string
}

func (memoryGraphHandler MemoryGraphHandler) HandleGetMemoryGraph(responseWriter http.ResponseWriter, request *http.Request) {
	graph := memory.MemoryGraph{}
	if memoryGraphHandler.MemoryService != nil {
		graph.Health = memoryGraphHandler.MemoryService.Health(request.Context())
	}
	if memoryGraphHandler.Reporter == nil {
		writeJSON(responseWriter, http.StatusOK, graph)
		return
	}

	reportGraph, errorValue := memoryGraphHandler.Reporter.ListMemoryGraph(request.Context(), memoryGraphLimit(request))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	reportGraph.Health = graph.Health
	reportGraph = memoryGraphHandler.filterGraphForReader(request, reportGraph)
	reportGraph = memoryGraphHandler.attachSearchFacts(request, reportGraph)
	reportGraph = memoryGraphHandler.attachPinnedFacts(request, reportGraph)
	reportGraph = rebuildMemoryGraphTopology(reportGraph)
	writeJSON(responseWriter, http.StatusOK, reportGraph)
}

func (memoryGraphHandler MemoryGraphHandler) HandleListPinnedPeople(responseWriter http.ResponseWriter, request *http.Request) {
	if memoryGraphHandler.MarkdownStore == nil {
		writeJSON(responseWriter, http.StatusOK, map[string]any{"pinned": []memory.MemoryFact{}})
		return
	}
	pinnedFacts, errorValue := memoryGraphHandler.MarkdownStore.ListPinnedMemories(request.Context())
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"pinned": pinnedFacts})
}

func (memoryGraphHandler MemoryGraphHandler) HandleDeleteEpisode(responseWriter http.ResponseWriter, request *http.Request) {
	var body struct {
		EpisodeID      string   `json:"episodeID"`
		NamespaceIDs   []string `json:"namespaceIDs"`
		ReaderPersonID string   `json:"readerPersonID"`
	}
	if errorValue := json.NewDecoder(request.Body).Decode(&body); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	episodeID := strings.TrimSpace(body.EpisodeID)
	readerPersonID := strings.TrimSpace(body.ReaderPersonID)
	if episodeID == "" || readerPersonID == "" {
		http.Error(responseWriter, "episodeID and readerPersonID must not be blank", http.StatusBadRequest)
		return
	}
	namespaceIDs, isFound, errorValue := memoryGraphHandler.readableEpisodeNamespaceIDs(request, episodeID, body.NamespaceIDs, readerPersonID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !isFound {
		http.Error(responseWriter, "episode not found", http.StatusNotFound)
		return
	}
	if len(namespaceIDs) == 0 {
		http.Error(responseWriter, "episode is not readable", http.StatusForbidden)
		return
	}
	if memoryGraphHandler.MemoryService == nil {
		http.Error(responseWriter, "memory service is not configured", http.StatusServiceUnavailable)
		return
	}
	result, errorValue := memoryGraphHandler.MemoryService.DeleteEpisode(request.Context(), memory.MemoryEpisodeDeleteRequest{
		EpisodeID:    episodeID,
		NamespaceIDs: namespaceIDs,
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (memoryGraphHandler MemoryGraphHandler) HandleUpdateFact(responseWriter http.ResponseWriter, request *http.Request) {
	readerPersonID := memoryGraphHandler.readReaderPersonID(request)
	var body memory.MemoryFactUpdateRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&body); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if errorValue := memoryGraphHandler.authorizeFactMutation(request, body.NamespaceID, body.FactID, readerPersonID); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), mutationStatus(errorValue))
		return
	}
	if memoryGraphHandler.MemoryService == nil {
		http.Error(responseWriter, "memory service is not configured", http.StatusServiceUnavailable)
		return
	}
	fact, errorValue := memoryGraphHandler.MemoryService.UpdateFact(request.Context(), body)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, fact)
}

func (memoryGraphHandler MemoryGraphHandler) HandleDeleteFact(responseWriter http.ResponseWriter, request *http.Request) {
	readerPersonID := memoryGraphHandler.readReaderPersonID(request)
	var body memory.MemoryFactDeleteRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&body); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if errorValue := memoryGraphHandler.authorizeFactMutation(request, body.NamespaceID, body.FactID, readerPersonID); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), mutationStatus(errorValue))
		return
	}
	if memoryGraphHandler.MemoryService == nil {
		http.Error(responseWriter, "memory service is not configured", http.StatusServiceUnavailable)
		return
	}
	result, errorValue := memoryGraphHandler.MemoryService.DeleteFact(request.Context(), body)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (memoryGraphHandler MemoryGraphHandler) readReaderPersonID(request *http.Request) string {
	if memoryGraphHandler.ReaderPersonID == nil {
		return ""
	}
	return strings.TrimSpace(memoryGraphHandler.ReaderPersonID(request))
}

func (memoryGraphHandler MemoryGraphHandler) authorizeFactMutation(request *http.Request, namespaceID string, factID string, readerPersonID string) error {
	if strings.TrimSpace(namespaceID) == "" || strings.TrimSpace(factID) == "" || readerPersonID == "" {
		return mutationError{http.StatusBadRequest, "reader identity and fact target are required"}
	}
	if memoryGraphHandler.Identity == nil {
		return mutationError{http.StatusServiceUnavailable, "memory graph authorization is not configured"}
	}
	reader, hasReader := memoryGraphHandler.Reporter.(memory.GraphMemoryEpisodeReader)
	if !hasReader {
		return mutationError{http.StatusServiceUnavailable, "memory graph reader is not configured"}
	}
	namespaces, errorValue := reader.ListMemoryGraphNamespacesByID(request.Context(), []string{namespaceID})
	if errorValue != nil {
		return errorValue
	}
	if len(namespaces) != 1 {
		return mutationError{http.StatusNotFound, "memory namespace not found"}
	}
	namespace := namespaces[0]
	graphNamespace := memory.MemoryGraphNamespace{NamespaceID: namespace.NamespaceID, ScopeType: namespace.ScopeType, ScopePersonID: namespace.ScopePersonID, ScopeConversationID: namespace.ScopeConversationID, ScopeCircleID: namespace.ScopeCircleID, SecurityLevelRank: namespace.SecurityLevelRank, RequiredClasses: namespace.RequiredClasses}
	if !canReadNamespace(graphNamespace, readerPersonID, memoryGraphHandler.Identity.ResolvePersonAccess(readerPersonID)) {
		return mutationError{http.StatusForbidden, "memory namespace is not readable"}
	}
	return nil
}

type mutationError struct {
	status  int
	message string
}

func (errorValue mutationError) Error() string { return errorValue.message }
func mutationStatus(errorValue error) int {
	if typedError, ok := errorValue.(mutationError); ok {
		return typedError.status
	}
	return http.StatusInternalServerError
}

func (memoryGraphHandler MemoryGraphHandler) HandleSavePinnedMemory(responseWriter http.ResponseWriter, request *http.Request) {
	var body struct {
		ReaderPersonID string `json:"readerPersonID"`
		Content        string `json:"content"`
	}
	if errorValue := json.NewDecoder(request.Body).Decode(&body); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.ReaderPersonID) == "" {
		http.Error(responseWriter, "readerPersonID must not be blank", http.StatusBadRequest)
		return
	}
	if memoryGraphHandler.MarkdownStore == nil {
		http.Error(responseWriter, "markdown memory is not configured", http.StatusServiceUnavailable)
		return
	}
	wasUpdated, errorValue := memoryGraphHandler.MarkdownStore.SavePersonMemory(request.Context(), body.ReaderPersonID, body.Content)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"updated": wasUpdated})
}

func (memoryGraphHandler MemoryGraphHandler) HandleDeletePinnedMemory(responseWriter http.ResponseWriter, request *http.Request) {
	var body struct {
		ReaderPersonID string `json:"readerPersonID"`
	}
	if errorValue := json.NewDecoder(request.Body).Decode(&body); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.ReaderPersonID) == "" {
		http.Error(responseWriter, "readerPersonID must not be blank", http.StatusBadRequest)
		return
	}
	if memoryGraphHandler.MarkdownStore == nil {
		http.Error(responseWriter, "markdown memory is not configured", http.StatusServiceUnavailable)
		return
	}
	wasDeleted, errorValue := memoryGraphHandler.MarkdownStore.DeletePersonMemory(request.Context(), body.ReaderPersonID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"deleted": wasDeleted})
}

func (memoryGraphHandler MemoryGraphHandler) readableEpisodeNamespaceIDs(
	request *http.Request,
	episodeID string,
	requestedNamespaceIDs []string,
	readerPersonID string,
) ([]string, bool, error) {
	reader, hasReader := memoryGraphHandler.Reporter.(memory.GraphMemoryEpisodeReader)
	if !hasReader || memoryGraphHandler.Identity == nil {
		return nil, false, nil
	}
	episode, isFound, errorValue := reader.GetMemoryGraphEpisode(request.Context(), episodeID)
	if errorValue != nil || !isFound {
		return nil, isFound, errorValue
	}
	namespaceIDs := requestedReadableNamespaceCandidates(episode.NamespaceIDs, requestedNamespaceIDs)
	namespaces, errorValue := reader.ListMemoryGraphNamespacesByID(request.Context(), namespaceIDs)
	if errorValue != nil {
		return nil, true, errorValue
	}
	personAccess := memoryGraphHandler.Identity.ResolvePersonAccess(readerPersonID)
	readableNamespaceIDs := []string{}
	for _, namespace := range namespaces {
		if canReadNamespace(namespace, readerPersonID, personAccess) {
			readableNamespaceIDs = append(readableNamespaceIDs, namespace.NamespaceID)
		}
	}
	return readableNamespaceIDs, true, nil
}

func requestedReadableNamespaceCandidates(episodeNamespaceIDs []string, requestedNamespaceIDs []string) []string {
	episodeNamespaceSet := memoryStringSet(episodeNamespaceIDs)
	requestedNamespaceSet := memoryStringSet(requestedNamespaceIDs)
	if len(requestedNamespaceSet) == 0 {
		return episodeNamespaceIDs
	}
	namespaceIDs := []string{}
	for _, namespaceID := range episodeNamespaceIDs {
		if requestedNamespaceSet[namespaceID] && episodeNamespaceSet[namespaceID] {
			namespaceIDs = append(namespaceIDs, namespaceID)
		}
	}
	return namespaceIDs
}

func memoryStringSet(values []string) map[string]bool {
	valueSet := map[string]bool{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			valueSet[trimmedValue] = true
		}
	}
	return valueSet
}

func (memoryGraphHandler MemoryGraphHandler) HandleMigrateIdentity(responseWriter http.ResponseWriter, request *http.Request) {
	var body struct {
		Mappings []memory.MemoryIdentityMapping `json:"mappings"`
	}
	if errorValue := json.NewDecoder(request.Body).Decode(&body); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if len(body.Mappings) == 0 {
		http.Error(responseWriter, "mappings must not be empty", http.StatusBadRequest)
		return
	}
	for _, mapping := range body.Mappings {
		if strings.TrimSpace(mapping.OldPersonID) == "" || strings.TrimSpace(mapping.NewPersonID) == "" {
			http.Error(responseWriter, "oldPersonID and newPersonID must not be blank", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(mapping.OldPersonID) == strings.TrimSpace(mapping.NewPersonID) {
			http.Error(responseWriter, "oldPersonID and newPersonID must differ", http.StatusBadRequest)
			return
		}
	}

	if memoryGraphHandler.Migrator == nil {
		http.Error(responseWriter, "memory migrator is not configured", http.StatusServiceUnavailable)
		return
	}

	results, errorValue := memoryGraphHandler.Migrator.MigrateMemoryIdentities(request.Context(), body.Mappings)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}

	for index, mapping := range body.Mappings {
		renamed, conflict, renameError := memoryGraphHandler.MarkdownStore.RenamePersonDirectory(mapping.OldPersonID, mapping.NewPersonID)
		if renameError != nil {
			http.Error(responseWriter, renameError.Error(), http.StatusInternalServerError)
			return
		}
		results[index].MarkdownRenamed = renamed
		results[index].MarkdownConflict = conflict
	}

	writeJSON(responseWriter, http.StatusOK, map[string]any{"results": results})
}

func (memoryGraphHandler MemoryGraphHandler) attachPinnedFacts(request *http.Request, graph memory.MemoryGraph) memory.MemoryGraph {
	if strings.TrimSpace(request.URL.Query().Get("query")) != "" {
		return graph
	}
	if memoryGraphHandler.MarkdownStore == nil {
		return graph
	}
	readerPersonID := strings.TrimSpace(request.URL.Query().Get("readerPersonID"))
	if readerPersonID == "" {
		return graph
	}
	pinnedFacts, errorValue := memoryGraphHandler.MarkdownStore.LoadPinnedMemory(request.Context(), readerPersonID)
	if errorValue != nil {
		return graph
	}
	graph.Facts = appendUniqueFacts(graph.Facts, pinnedFacts)
	return graph
}

func appendUniqueFacts(facts []memory.MemoryFact, additions []memory.MemoryFact) []memory.MemoryFact {
	seenFactKeys := map[string]bool{}
	for _, fact := range facts {
		seenFactKeys[fact.NamespaceID+":"+fact.FactID+":"+fact.Content] = true
	}
	for _, fact := range additions {
		key := fact.NamespaceID + ":" + fact.FactID + ":" + fact.Content
		if seenFactKeys[key] {
			continue
		}
		seenFactKeys[key] = true
		facts = append(facts, fact)
	}
	return facts
}

func (memoryGraphHandler MemoryGraphHandler) filterGraphForReader(request *http.Request, graph memory.MemoryGraph) memory.MemoryGraph {
	readerPersonID := strings.TrimSpace(request.URL.Query().Get("readerPersonID"))
	if readerPersonID == "" {
		return graph
	}
	if memoryGraphHandler.Identity == nil {
		graph.Namespaces = []memory.MemoryGraphNamespace{}
		graph.Episodes = []memory.MemoryGraphEpisode{}
		graph.Nodes = []memory.MemoryGraphNode{}
		graph.Edges = []memory.MemoryGraphEdge{}
		return graph
	}
	personAccess := memoryGraphHandler.Identity.ResolvePersonAccess(readerPersonID)
	namespaces := filterReaderNamespaces(graph.Namespaces, readerPersonID, personAccess)
	namespaceSet := memoryNamespaceIDSet(namespaces)
	graph.Namespaces = namespaces
	graph.Episodes = filterReaderEpisodes(graph.Episodes, namespaceSet)
	graph.Nodes = []memory.MemoryGraphNode{}
	graph.Edges = []memory.MemoryGraphEdge{}
	return graph
}

func (memoryGraphHandler MemoryGraphHandler) attachSearchFacts(request *http.Request, graph memory.MemoryGraph) memory.MemoryGraph {
	if memoryGraphHandler.MemoryService == nil {
		return graph
	}
	query := strings.TrimSpace(request.URL.Query().Get("query"))
	limit := memoryGraphLimit(request)
	facts, failures := memoryGraphHandler.collectNamespaceFacts(request, graph.Namespaces, query, limit)
	graph.Facts = facts
	graph.Retrieval = memory.MemoryRetrieval{Query: query, Complete: len(failures) == 0 && len(facts) < limit, Limit: limit, Failures: failures}
	return graph
}

func (memoryGraphHandler MemoryGraphHandler) collectNamespaceFacts(request *http.Request, namespaces []memory.MemoryGraphNamespace, query string, limit int) ([]memory.MemoryFact, []memory.MemoryRetrievalFailure) {
	facts := []memory.MemoryFact{}
	failures := []memory.MemoryRetrievalFailure{}
	seenFactKeys := map[string]bool{}
	for _, namespace := range namespaces {
		searchRequest := memory.MemorySearchRequest{
			Query:                   query,
			ReaderPersonID:          namespace.ScopePersonID,
			ReaderCircles:           namespaceReaderCircles(namespace),
			ReaderSecurityLevelRank: namespace.SecurityLevelRank,
			ReaderGrantedClasses:    namespace.RequiredClasses,
			Namespaces:              []memory.MemoryNamespace{memoryNamespaceFromGraphNamespace(namespace)},
			ExplicitNamespacesOnly:  true,
			Limit:                   limit,
		}
		namespaceFacts, errorValue := memoryGraphHandler.namespaceFacts(request, query, searchRequest)
		if errorValue != nil {
			failures = append(failures, memory.MemoryRetrievalFailure{NamespaceID: namespace.NamespaceID, Message: safeRetrievalFailureMessage(errorValue)})
			continue
		}
		for _, fact := range namespaceFacts {
			key := fact.NamespaceID + ":" + fact.FactID + ":" + fact.Content
			if key == "::" || seenFactKeys[key] {
				continue
			}
			seenFactKeys[key] = true
			facts = append(facts, fact)
			if len(facts) >= limit {
				return facts, failures
			}
		}
	}
	return facts, failures
}

func safeRetrievalFailureMessage(errorValue error) string {
	message := strings.Join(strings.Fields(errorValue.Error()), " ")
	if len(message) > 240 {
		return "memory retrieval failed"
	}
	return message
}

func (memoryGraphHandler MemoryGraphHandler) namespaceFacts(request *http.Request, query string, searchRequest memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	if query == "" {
		return memoryGraphHandler.MemoryService.ListMemory(request.Context(), searchRequest)
	}
	return memoryGraphHandler.MemoryService.SearchMemory(request.Context(), searchRequest)
}

func filterReaderNamespaces(namespaces []memory.MemoryGraphNamespace, readerPersonID string, personAccess policy.PersonAccess) []memory.MemoryGraphNamespace {
	filteredNamespaces := []memory.MemoryGraphNamespace{}
	for _, namespace := range namespaces {
		if canReadNamespace(namespace, readerPersonID, personAccess) {
			filteredNamespaces = append(filteredNamespaces, namespace)
		}
	}
	return filteredNamespaces
}

func canReadNamespace(namespace memory.MemoryGraphNamespace, readerPersonID string, personAccess policy.PersonAccess) bool {
	switch namespace.ScopeType {
	case memory.ScopeTypeUser, memory.ScopeTypePrivate:
		return strings.TrimSpace(namespace.ScopePersonID) == strings.TrimSpace(readerPersonID)
	case memory.ScopeTypeCircle:
		return containsMemoryCircle(personAccess.Circles, namespace.ScopeCircleID)
	case memory.ScopeTypeWorkspace:
		return personAccess.SecurityLevelRank >= namespace.SecurityLevelRank && containsMemoryClasses(personAccess.GrantedClasses, namespace.RequiredClasses)
	default:
		return false
	}
}

func filterReaderEpisodes(episodes []memory.MemoryGraphEpisode, namespaceSet map[string]bool) []memory.MemoryGraphEpisode {
	filteredEpisodes := []memory.MemoryGraphEpisode{}
	for _, episode := range episodes {
		namespaceIDs := []string{}
		for _, namespaceID := range episode.NamespaceIDs {
			if namespaceSet[namespaceID] {
				namespaceIDs = append(namespaceIDs, namespaceID)
			}
		}
		if len(namespaceIDs) == 0 {
			continue
		}
		episode.NamespaceIDs = namespaceIDs
		filteredEpisodes = append(filteredEpisodes, episode)
	}
	return filteredEpisodes
}

func memoryNamespaceIDSet(namespaces []memory.MemoryGraphNamespace) map[string]bool {
	namespaceSet := map[string]bool{}
	for _, namespace := range namespaces {
		namespaceSet[namespace.NamespaceID] = true
	}
	return namespaceSet
}

func containsMemoryCircle(circles []string, circleID string) bool {
	for _, value := range circles {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(circleID)) {
			return true
		}
	}
	return false
}

func containsMemoryClasses(grantedClasses []string, requiredClasses []string) bool {
	grantedSet := map[string]bool{}
	for _, grantedClass := range grantedClasses {
		grantedSet[strings.TrimSpace(grantedClass)] = true
	}
	for _, requiredClass := range requiredClasses {
		if !grantedSet[strings.TrimSpace(requiredClass)] {
			return false
		}
	}
	return true
}

func rebuildMemoryGraphTopology(graph memory.MemoryGraph) memory.MemoryGraph {
	nodes := []memory.MemoryGraphNode{}
	edges := []memory.MemoryGraphEdge{}
	for _, namespace := range graph.Namespaces {
		nodes = append(nodes, memory.MemoryGraphNode{
			NodeID:    "namespace:" + namespace.NamespaceID,
			Label:     namespace.NamespaceID,
			Kind:      "namespace",
			ScopeType: namespace.ScopeType,
		})
	}
	for _, episode := range graph.Episodes {
		episodeNodeID := "episode:" + episode.EpisodeID
		nodes = append(nodes, memory.MemoryGraphNode{
			NodeID: episodeNodeID,
			Label:  episode.Platform + " " + episode.OccurredAt.Format("01-02 15:04"),
			Kind:   "episode",
			Status: episode.IngestionStatus,
		})
		for _, namespaceID := range episode.NamespaceIDs {
			edges = append(edges, memory.MemoryGraphEdge{
				SourceID: "namespace:" + namespaceID,
				TargetID: episodeNodeID,
				Label:    "episode",
				Weight:   1,
			})
		}
	}
	for _, fact := range graph.Facts {
		factNodeID := "fact:" + fact.NamespaceID + ":" + fact.FactID
		nodes = append(nodes, memory.MemoryGraphNode{
			NodeID:    factNodeID,
			Label:     compactGraphLabel(fact.Content),
			Kind:      "fact",
			ScopeType: fact.ScopeType,
			Status:    fact.SourceKind,
		})
		edges = append(edges, memory.MemoryGraphEdge{
			SourceID: "namespace:" + fact.NamespaceID,
			TargetID: factNodeID,
			Label:    "fact",
			Weight:   2,
		})
	}
	graph.Nodes = nodes
	graph.Edges = edges
	return graph
}

func memoryGraphLimit(request *http.Request) int {
	limit, errorValue := strconv.Atoi(strings.TrimSpace(request.URL.Query().Get("limit")))
	if errorValue != nil || limit <= 0 {
		return 80
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func namespaceReaderCircles(namespace memory.MemoryGraphNamespace) []string {
	if namespace.ScopeCircleID == "" {
		return []string{}
	}
	return []string{namespace.ScopeCircleID}
}

func memoryNamespaceFromGraphNamespace(namespace memory.MemoryGraphNamespace) memory.MemoryNamespace {
	return memory.MemoryNamespace{
		NamespaceID:         namespace.NamespaceID,
		ScopeType:           namespace.ScopeType,
		ScopePersonID:       namespace.ScopePersonID,
		ScopeConversationID: namespace.ScopeConversationID,
		ScopeCircleID:       namespace.ScopeCircleID,
		SecurityLevelRank:   namespace.SecurityLevelRank,
		RequiredClasses:     namespace.RequiredClasses,
	}
}

func compactGraphLabel(value string) string {
	runes := []rune(strings.Join(strings.Fields(value), " "))
	if len(runes) <= 56 {
		return string(runes)
	}
	return string(runes[:56]) + "..."
}
