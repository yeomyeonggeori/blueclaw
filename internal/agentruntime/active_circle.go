package agentruntime

import (
	"regexp"
	"sort"
	"strings"
)

func withResolvedActiveCircle(request ToolCatalogRequest) ToolCatalogRequest {
	if strings.TrimSpace(request.ActiveCircleID) != "" || request.ActiveCircleConflict {
		request.ActiveCircleID = strings.ToLower(strings.TrimSpace(request.ActiveCircleID))
		return request
	}
	activeCircleID, hasConflict := ResolveActiveCircleID(request)
	request.ActiveCircleID = activeCircleID
	request.ActiveCircleConflict = hasConflict
	return request
}

func ResolveActiveCircleID(request ToolCatalogRequest) (string, bool) {
	circleIDs := map[string]bool{}
	if circleID := circleIDFromConversation(request); circleID != "" {
		circleIDs[circleID] = true
	}
	for _, circleID := range mentionedCircleIDs(request.Prompt, request.PersonAccess.Circles) {
		circleIDs[circleID] = true
	}
	if len(circleIDs) == 0 {
		return "", false
	}
	if len(circleIDs) > 1 {
		return "", true
	}
	for circleID := range circleIDs {
		return circleID, false
	}
	return "", false
}

func mentionedCircleIDs(prompt string, circleIDs []string) []string {
	normalizedPrompt := strings.TrimSpace(prompt)
	if normalizedPrompt == "" {
		return nil
	}
	mentionedCircleIDs := []string{}
	for _, circleID := range normalizedCircleIDs(circleIDs) {
		if circleMentionPattern(circleID).MatchString(normalizedPrompt) {
			mentionedCircleIDs = append(mentionedCircleIDs, circleID)
		}
	}
	sort.Strings(mentionedCircleIDs)
	return mentionedCircleIDs
}

func normalizedCircleIDs(circleIDs []string) []string {
	seenCircleIDs := map[string]bool{}
	normalizedCircleIDs := []string{}
	for _, circleID := range circleIDs {
		normalizedCircleID := strings.ToLower(strings.TrimSpace(circleID))
		if normalizedCircleID == "" || seenCircleIDs[normalizedCircleID] {
			continue
		}
		seenCircleIDs[normalizedCircleID] = true
		normalizedCircleIDs = append(normalizedCircleIDs, normalizedCircleID)
	}
	return normalizedCircleIDs
}

func circleMentionPattern(circleID string) *regexp.Regexp {
	escapedCircleID := regexp.QuoteMeta(strings.ToLower(strings.TrimSpace(circleID)))
	return regexp.MustCompile(`(?i)(^|[^A-Za-z0-9._-])@` + escapedCircleID + `($|[^A-Za-z0-9._-])`)
}
