package memory

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluememo"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

const RememberContentRuneLimit = 600

type ContainedCircleResolver interface {
	ContainedCircles() map[string][]string
}

func ReaderForAccess(personAccess policy.PersonAccess, containedCircles map[string][]string) bluememo.Reader {
	return bluememo.NewReader(personAccess.PersonID, personAccess.Circles, containedCircles, personAccess.SecurityLevelRank, personAccess.GrantedClasses)
}

func LabelForAccess(personAccess policy.PersonAccess) bluememo.SecurityLabel {
	return bluememo.SecurityLabel{SecurityLevelRank: personAccess.SecurityLevelRank, RequiredClasses: append([]string{}, personAccess.GrantedClasses...)}
}

func LabelForConversation(personAccess policy.PersonAccess, channelPolicy policy.ChannelPolicy, isChannelPolicyFound bool) bluememo.SecurityLabel {
	if isChannelPolicyFound {
		return bluememo.SecurityLabel{SecurityLevelRank: channelPolicy.DefaultSecurityLevelRank, RequiredClasses: append([]string{}, channelPolicy.DefaultRequiredClasses...)}
	}
	return LabelForAccess(personAccess)
}

type LanguageModel struct {
	Provider model.LanguageModelProvider
}

func (languageModel LanguageModel) GenerateStructured(ctx context.Context, request bluememo.StructuredRequest) (string, error) {
	response, errorValue := languageModel.Provider.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: request.Instruction},
			{Role: "user", Content: request.Subject},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               request.SchemaName,
			Document:           request.SchemaDocument,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return "", errorValue
	}
	return response.Content, nil
}

func RememberContentGateMessage(content string) string {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return "memory_remember content is required"
	}
	if len([]rune(trimmedContent)) > RememberContentRuneLimit {
		return "memory_remember content must be a single compact fact"
	}
	return ""
}

func LoopMemoryFacts(recall bluememo.Recall, requesterPersonID string) []MemoryFact {
	facts := make([]MemoryFact, 0, len(recall.ProfileLines())+len(recall.Facts))
	for index, line := range recall.ProfileLines() {
		facts = append(facts, MemoryFact{
			FactID:     "profile:" + requesterPersonID + ":" + string(rune('a'+index)),
			ScopeType:  bluememo.ScopeTypePrivate,
			Content:    line,
			SourceKind: "profile",
			ValidAt:    recall.Profile.BuiltAt,
		})
	}
	for _, scoredFact := range recall.Facts {
		facts = append(facts, MemoryFact{
			FactID:            scoredFact.Fact.FactID,
			ScopeType:         scoredFact.Fact.ScopeType,
			Content:           scoredFact.Fact.Content,
			Score:             scoredFact.Score,
			SourceEpisodeID:   scoredFact.Fact.EpisodeID,
			SourceKind:        scoredFact.Fact.Kind,
			ValidAt:           scoredFact.Fact.ValidFrom,
			SecurityLevelRank: scoredFact.Fact.SecurityLevelRank,
			RequiredClasses:   append([]string{}, scoredFact.Fact.RequiredClasses...),
		})
	}
	return facts
}
