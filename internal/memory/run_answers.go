package memory

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

// runAnswers is the layer of what a task run's tools answered. The record a
// run read and the changes it made live in the systems that answered, so a
// memory restating one would be a second copy that goes stale the day the
// record moves. Ranking here only orders what the judge is shown; the judge
// decides whether a statement says what an answer said.
type runAnswers []bluememo.Memory

func answersOf(events []agentcontract.TaskEvent) runAnswers {
	answers := runAnswers{}
	for _, event := range events {
		if _, isResult := agentcontract.ToolTaskEventToolName(event.Name, agentcontract.ToolTaskEventResultSuffix); !isResult {
			continue
		}
		var observation struct {
			ObservationID string          `json:"observationID"`
			Summary       string          `json:"summary"`
			Failure       json.RawMessage `json:"failure"`
		}
		if json.Unmarshal([]byte(event.Body), &observation) != nil || hasFailure(observation.Failure) {
			continue
		}
		if summary := strings.TrimSpace(observation.Summary); summary != "" {
			answers = append(answers, bluememo.Memory{MemoryID: "answer-" + observation.ObservationID, Content: summary})
		}
	}
	return answers
}

func hasFailure(failure json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(failure))
	return trimmed != "" && trimmed != "null"
}

func (answers runAnswers) Search(_ context.Context, query string, limit int) ([]bluememo.RecalledMemory, error) {
	queryPairs := runePairs(query)
	ranked := []bluememo.RecalledMemory{}
	for _, answer := range answers {
		if shared := sharedCount(queryPairs, runePairs(answer.Content)); shared > 0 {
			ranked = append(ranked, bluememo.RecalledMemory{Memory: answer, Score: float64(shared) / float64(len(queryPairs))})
		}
	}
	sort.SliceStable(ranked, func(first, second int) bool { return ranked[first].Score > ranked[second].Score })
	return ranked[:min(len(ranked), limit)], nil
}

func runePairs(text string) map[string]bool {
	runes := []rune(strings.ToLower(strings.Join(strings.Fields(text), " ")))
	pairs := map[string]bool{}
	for index := 0; index+1 < len(runes); index++ {
		pairs[string(runes[index:index+2])] = true
	}
	return pairs
}

func sharedCount(first map[string]bool, second map[string]bool) int {
	count := 0
	for pair := range first {
		if second[pair] {
			count++
		}
	}
	return count
}
