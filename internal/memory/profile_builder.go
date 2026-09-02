package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	profileIdentityLineLimit = 8
	profileCurrentLineLimit  = 6
	profileCurrentWindow     = 30 * 24 * time.Hour
)

const profileSchemaDocument = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["identityLines", "currentLines"],
  "properties": {
    "identityLines": {"type": "array", "maxItems": 8, "items": {"type": "string", "maxLength": 240}},
    "currentLines": {"type": "array", "maxItems": 6, "items": {"type": "string", "maxLength": 240}}
  }
}`

const profileInstruction = `You condense the memory facts an assistant holds about one person into a short profile the assistant reads before every task.

identityLines: who the person is and how they want things done, drawn from identity and preference facts. At most 8 lines.
currentLines: what the person is working on or dealing with right now, drawn from the recent facts, episodes, and time-bound states listed, most recent first. At most 6 lines.

Each line is one sentence in the language the facts use, naming the person. Merge facts that say the same thing. Use only the facts given: do not add, guess, or extend them, and return empty lists when the facts hold nothing for a section.`

type ProfileBuilder struct {
	Store Store
	Model model.LanguageModelProvider
	Now   func() time.Time
}

type profileOutput struct {
	IdentityLines []string `json:"identityLines"`
	CurrentLines  []string `json:"currentLines"`
}

func (builder ProfileBuilder) Rebuild(ctx context.Context, personID string) (Profile, error) {
	if builder.Store.Facts == nil || builder.Store.Profiles == nil {
		return Profile{}, errors.New("memory profile builder has no repositories")
	}
	if builder.Model == nil {
		return Profile{}, errors.New("memory profile builder has no language model")
	}
	now := builder.now()
	facts, errorValue := builder.Store.Facts.ListLiveFactsAboutPerson(ctx, personID, now)
	if errorValue != nil {
		return Profile{}, errorValue
	}
	profile := Profile{PersonID: personID, IdentityLines: []string{}, CurrentLines: []string{}, BuiltFromFactCount: len(facts), BuiltAt: now}
	if len(facts) > 0 {
		output, errorValue := builder.askModel(ctx, facts, now)
		if errorValue != nil {
			return Profile{}, errorValue
		}
		profile.IdentityLines = cleanProfileLines(output.IdentityLines, profileIdentityLineLimit)
		profile.CurrentLines = cleanProfileLines(output.CurrentLines, profileCurrentLineLimit)
	}
	if errorValue := builder.Store.Profiles.SaveProfile(ctx, profile); errorValue != nil {
		return Profile{}, errorValue
	}
	return profile, nil
}

func (builder ProfileBuilder) askModel(ctx context.Context, facts []Fact, now time.Time) (profileOutput, error) {
	response, errorValue := builder.Model.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: profileInstruction},
			{Role: "user", Content: profileSubject(facts, now)},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               "memory_profile",
			Document:           profileSchemaDocument,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return profileOutput{}, fmt.Errorf("memory profile model call failed: %w", errorValue)
	}
	var output profileOutput
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(response.Content)), &output); errorValue != nil {
		return profileOutput{}, TerminalJobError{Cause: fmt.Errorf("memory profile output is not the schema: %w", errorValue)}
	}
	return output, nil
}

func profileSubject(facts []Fact, now time.Time) string {
	stable := []string{}
	recent := []string{}
	for _, fact := range facts {
		line := fmt.Sprintf("- [%s, %s] %s", fact.Kind, fact.ValidFrom.Format("2006-01-02"), fact.Content)
		if !fact.ValidUntil.IsZero() {
			line += " (until " + fact.ValidUntil.Format("2006-01-02") + ")"
		}
		if fact.Kind == FactKindIdentity || fact.Kind == FactKindPreference {
			stable = append(stable, line)
			continue
		}
		if now.Sub(fact.ValidFrom) <= profileCurrentWindow {
			recent = append(recent, line)
		}
	}
	lines := []string{"Today: " + now.Format("2006-01-02"), "", "Identity and preference facts:"}
	lines = append(lines, orNone(stable)...)
	lines = append(lines, "", "Facts from the last 30 days, most recent first:")
	lines = append(lines, orNone(recent)...)
	return strings.Join(lines, "\n")
}

func orNone(lines []string) []string {
	if len(lines) == 0 {
		return []string{"(none)"}
	}
	return lines
}

func cleanProfileLines(lines []string, limit int) []string {
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.Join(strings.Fields(line), " ")
		if trimmed == "" {
			continue
		}
		if runes := []rune(trimmed); len(runes) > FactContentCharacterLimit {
			trimmed = string(runes[:FactContentCharacterLimit])
		}
		cleaned = append(cleaned, trimmed)
		if len(cleaned) == limit {
			break
		}
	}
	return cleaned
}

func (builder ProfileBuilder) now() time.Time {
	if builder.Now != nil {
		return builder.Now().UTC()
	}
	return time.Now().UTC()
}
