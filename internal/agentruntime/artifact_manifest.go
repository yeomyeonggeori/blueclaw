package agentruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const artifactManifestEntryLimit = 10

type artifactManifestCandidate struct {
	taskRunID      string
	path           string
	producingTool  string
	producingSkill string
}

func buildConversationArtifactManifest(request agentcontract.AgentTurnRequest, taskRunService taskstate.TaskRunStore, taskArtifactService taskstate.TaskArtifactStore) []agentcontract.ArtifactManifestEntry {
	if taskRunService == nil || strings.TrimSpace(request.ConversationID) == "" {
		return nil
	}
	candidates := []artifactManifestCandidate{}
	for _, taskRun := range conversationArtifactTaskRuns(request, taskRunService.ListTaskRun()) {
		taskEvents := taskRunService.ListTaskEvent(taskRun.TaskRunID)
		producingSkill := selectedSkillNameFromTaskEvents(taskEvents)
		candidates = append(candidates, artifactManifestCandidatesFromTaskEvents(taskRun.TaskRunID, taskEvents, producingSkill)...)
		if taskArtifactService != nil {
			taskArtifacts := taskArtifactService.ListTaskArtifact(taskRun.TaskRunID)
			candidates = append(candidates, artifactManifestCandidatesFromTaskArtifacts(taskRun.TaskRunID, taskArtifacts, producingSkill)...)
		}
	}
	return boundedArtifactManifestEntries(request, candidates)
}

func conversationArtifactTaskRuns(request agentcontract.AgentTurnRequest, taskRuns []taskstate.TaskRun) []taskstate.TaskRun {
	trimmedConversationID := strings.TrimSpace(request.ConversationID)
	currentTaskRunID := strings.TrimSpace(request.ExistingTaskRunID)
	matchingTaskRuns := []taskstate.TaskRun{}
	for _, taskRun := range taskRuns {
		if strings.TrimSpace(taskRun.OriginConversationID) != trimmedConversationID {
			continue
		}
		if currentTaskRunID != "" && strings.TrimSpace(taskRun.TaskRunID) == currentTaskRunID {
			continue
		}
		matchingTaskRuns = append(matchingTaskRuns, taskRun)
	}
	return matchingTaskRuns
}

func artifactManifestCandidatesFromTaskEvents(taskRunID string, taskEvents []taskstate.TaskEvent, producingSkill string) []artifactManifestCandidate {
	candidates := []artifactManifestCandidate{}
	for _, taskEvent := range taskEvents {
		toolName := artifactManifestToolNameFromEvent(taskEvent.Name)
		if toolName == "" {
			continue
		}
		candidates = append(candidates, artifactManifestCandidatesFromToolBody(taskRunID, taskEvent.Body, toolName, producingSkill)...)
	}
	return candidates
}

func artifactManifestCandidatesFromTaskArtifacts(taskRunID string, taskArtifacts []taskstate.TaskArtifact, producingSkill string) []artifactManifestCandidate {
	candidates := []artifactManifestCandidate{}
	for _, taskArtifact := range taskArtifacts {
		toolName := artifactManifestToolNameFromEvent(taskArtifact.Name)
		candidates = append(candidates, artifactManifestCandidatesFromToolBody(taskRunID, taskArtifact.Body, toolName, producingSkill)...)
	}
	return candidates
}

func artifactManifestToolNameFromEvent(name string) string {
	trimmedName := strings.TrimSpace(name)
	if !strings.HasPrefix(trimmedName, "tool.") || !strings.HasSuffix(trimmedName, ".result") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(trimmedName, "tool."), ".result")
}

func artifactManifestCandidatesFromToolBody(taskRunID string, body string, toolName string, producingSkill string) []artifactManifestCandidate {
	paths := artifactManifestPathsFromBody(body)
	candidates := []artifactManifestCandidate{}
	for _, path := range paths {
		candidates = append(candidates, artifactManifestCandidate{
			taskRunID:      strings.TrimSpace(taskRunID),
			path:           path,
			producingTool:  strings.TrimSpace(toolName),
			producingSkill: strings.TrimSpace(producingSkill),
		})
	}
	return candidates
}

func artifactManifestPathsFromBody(body string) []string {
	var document any
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &document) != nil {
		return artifactManifestPathsFromText(body)
	}
	paths := artifactManifestPathsFromValue(document)
	return append(paths, artifactManifestPathsFromText(body)...)
}

func artifactManifestPathsFromValue(value any) []string {
	switch typedValue := value.(type) {
	case map[string]any:
		return artifactManifestPathsFromMap(typedValue)
	case []any:
		paths := []string{}
		for _, item := range typedValue {
			paths = append(paths, artifactManifestPathsFromValue(item)...)
		}
		return paths
	case string:
		if isArtifactManifestPath(typedValue) {
			return []string{typedValue}
		}
		return artifactManifestPathsFromText(typedValue)
	default:
		return nil
	}
}

func artifactManifestPathsFromMap(document map[string]any) []string {
	paths := []string{}
	for key, value := range document {
		if artifactManifestPathKey(key) {
			if path, isString := value.(string); isString && isArtifactManifestPath(path) {
				paths = append(paths, path)
				continue
			}
		}
		paths = append(paths, artifactManifestPathsFromValue(value)...)
	}
	return paths
}

func artifactManifestPathKey(key string) bool {
	switch strings.TrimSpace(key) {
	case "path", "devicePath", "relativePath":
		return true
	default:
		return false
	}
}

func artifactManifestPathsFromText(text string) []string {
	replacer := strings.NewReplacer("\"", " ", "'", " ", "`", " ", ",", " ", "}", " ", "]", " ", "\n", " ", "\t", " ")
	paths := []string{}
	for _, token := range strings.Fields(replacer.Replace(text)) {
		trimmedToken := strings.TrimSpace(token)
		if isArtifactManifestPath(trimmedToken) {
			paths = append(paths, trimmedToken)
		}
	}
	return paths
}

func isArtifactManifestPath(path string) bool {
	normalizedPath := strings.TrimSpace(filepath.ToSlash(path))
	if normalizedPath == "" {
		return false
	}
	return strings.HasPrefix(normalizedPath, "artifacts/") ||
		strings.Contains(normalizedPath, "/artifacts/") ||
		strings.HasPrefix(normalizedPath, "/workspace/circles/") ||
		strings.HasPrefix(normalizedPath, "circles/") ||
		strings.HasPrefix(normalizedPath, "/workspace/shared/public/") ||
		strings.HasPrefix(normalizedPath, "shared/public/")
}

func boundedArtifactManifestEntries(request agentcontract.AgentTurnRequest, candidates []artifactManifestCandidate) []agentcontract.ArtifactManifestEntry {
	entriesByPath := map[string]agentcontract.ArtifactManifestEntry{}
	for _, candidate := range candidates {
		entry, isValid := artifactManifestEntryForCandidate(request, candidate)
		if !isValid {
			continue
		}
		existingEntry, isFound := entriesByPath[entry.RelativePath]
		if !isFound || entry.ModifiedAt.After(existingEntry.ModifiedAt) {
			entriesByPath[entry.RelativePath] = entry
		}
	}
	entries := make([]agentcontract.ArtifactManifestEntry, 0, len(entriesByPath))
	for _, entry := range entriesByPath {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(leftIndex int, rightIndex int) bool {
		if entries[leftIndex].ModifiedAt.Equal(entries[rightIndex].ModifiedAt) {
			return entries[leftIndex].RelativePath < entries[rightIndex].RelativePath
		}
		return entries[leftIndex].ModifiedAt.After(entries[rightIndex].ModifiedAt)
	})
	if len(entries) > artifactManifestEntryLimit {
		return entries[:artifactManifestEntryLimit]
	}
	return entries
}

func artifactManifestEntryForCandidate(request agentcontract.AgentTurnRequest, candidate artifactManifestCandidate) (agentcontract.ArtifactManifestEntry, bool) {
	concretePath := concreteArtifactManifestPath(request, candidate.path)
	if concretePath == "" {
		return agentcontract.ArtifactManifestEntry{}, false
	}
	fileInformation, errorValue := os.Stat(concretePath)
	if errorValue != nil || fileInformation.IsDir() {
		return agentcontract.ArtifactManifestEntry{}, false
	}
	relativePath := relativeWorkspacePath(request.WorkspaceRootPath, concretePath)
	return agentcontract.ArtifactManifestEntry{
		TaskRunID:      candidate.taskRunID,
		RelativePath:   filepath.ToSlash(relativePath),
		ProducingTool:  strings.TrimSpace(candidate.producingTool),
		ProducingSkill: strings.TrimSpace(candidate.producingSkill),
		ModifiedAt:     fileInformation.ModTime(),
	}, true
}

func concreteArtifactManifestPath(request agentcontract.AgentTurnRequest, path string) string {
	normalizedPath := strings.TrimPrefix(strings.TrimSpace(filepath.ToSlash(path)), "/workspace/")
	if filepath.IsAbs(path) && pathIsInsideDirectory(request.WorkspaceRootPath, path) {
		return path
	}
	if strings.HasPrefix(normalizedPath, "artifacts/") && strings.TrimSpace(request.WorkspaceDefaultPath) != "" {
		return filepath.Join(request.WorkspaceDefaultPath, filepath.FromSlash(normalizedPath))
	}
	if strings.TrimSpace(request.WorkspaceRootPath) == "" {
		return ""
	}
	return filepath.Join(request.WorkspaceRootPath, filepath.FromSlash(normalizedPath))
}

func pathIsInsideDirectory(directoryPath string, path string) bool {
	relativePath, errorValue := filepath.Rel(directoryPath, path)
	return errorValue == nil && relativePath != "." && !strings.HasPrefix(relativePath, "..")
}

func selectedSkillNameFromTaskEvents(taskEvents []taskstate.TaskEvent) string {
	for eventIndex := len(taskEvents) - 1; eventIndex >= 0; eventIndex-- {
		if strings.TrimSpace(taskEvents[eventIndex].Name) != "agent.instructions_loaded" {
			continue
		}
		if skillName := selectedSkillNameFromInstructionEvent(taskEvents[eventIndex].Body); skillName != "" {
			return skillName
		}
	}
	return ""
}

func selectedSkillNameFromInstructionEvent(body string) string {
	var document struct {
		SkillDecisions []agentcontract.SkillSelectionDecision `json:"skillDecisions"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &document) != nil {
		return ""
	}
	for _, skillDecision := range document.SkillDecisions {
		if strings.TrimSpace(skillDecision.Status) == "selected" && strings.TrimSpace(skillDecision.Name) != "" {
			return strings.TrimSpace(skillDecision.Name)
		}
	}
	return ""
}

func relativeWorkspacePath(workspaceRootPath string, path string) string {
	relativePath, errorValue := filepath.Rel(workspaceRootPath, path)
	if errorValue != nil || strings.HasPrefix(relativePath, "..") {
		return filepath.Base(path)
	}
	return relativePath
}
