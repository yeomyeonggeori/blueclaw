package inboundengagement

import (
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

const (
	QuestionRemember   = "remember"
	RememberOptionNone = "none"
)

func asksRemember(facts Facts) bool {
	return len(facts.MemoryLayers) > 0
}

func rememberQuestion(about string, agentName string, senderName string, layers []memory.Scope) model.DecisionQuestion {
	descriptions := map[string]string{
		RememberOptionNone: "nothing lasting to keep: a question, a greeting, chatter, an opinion, one-off coordination, a request that states no lasting fact, or a fact the message only implies",
	}
	for _, layer := range layers {
		descriptions[rememberOptionOf(layer)] = memoryLayerDescription(layer, agentName, senderName)
	}
	return model.ChoiceQuestion{
		Instructions: about + "Does it state something " + agentName + " should remember for the people in this conversation, whether or not it needs a reply or work? " +
			"Each option other than none is a memory read by exactly the people in this conversation. " +
			"Remember only a lasting fact the message itself states: a decision, a rule or convention, a standing arrangement, a preference, or a fact about a person, a team, or the company that stays true beyond this conversation. " +
			"Do not infer a fact the message does not state; its topic, tone, or wording alone is never a reason to remember it.",
		OptionDescriptions: descriptions,
	}.Question()
}

func rememberOptionOf(layer memory.Scope) string {
	switch layer.Kind {
	case memory.ScopeWorkspace:
		return "company"
	case memory.ScopeCircle:
		return "circle:" + layer.ID
	}
	return "person"
}

func memoryLayerDescription(layer memory.Scope, agentName string, senderName string) string {
	switch layer.Kind {
	case memory.ScopeWorkspace:
		return "a lasting fact everyone at the company should keep knowing, kept in the company's memory"
	case memory.ScopeCircle:
		return "a lasting fact the " + layer.ID + " circle should keep knowing, kept in that circle's memory"
	}
	return "a lasting fact about " + firstNonEmpty(senderName, "the person writing") + " that " + agentName + " should keep knowing for them, kept in their own memory"
}

// readRememberLayer keeps a message when the layers together outweigh none,
// in the likeliest of them, the way work is read: options that share a verdict
// are summed before they are compared with the one that refuses it.
func readRememberLayer(answer model.DecisionAnswer, layers []memory.Scope) memory.Scope {
	if len(answer.Probabilities) == 0 {
		return offeredLayer(strings.TrimSpace(answer.Choice), layers)
	}
	likeliest, likeliestWeight, keepingWeight := memory.Scope{}, 0.0, 0.0
	for _, layer := range layers {
		weight := answer.ChoiceProbability(rememberOptionOf(layer))
		keepingWeight += weight
		if weight > likeliestWeight {
			likeliest, likeliestWeight = layer, weight
		}
	}
	if keepingWeight <= answer.ChoiceProbability(RememberOptionNone) {
		return memory.Scope{}
	}
	return likeliest
}

func offeredLayer(option string, layers []memory.Scope) memory.Scope {
	for _, layer := range layers {
		if rememberOptionOf(layer) == option {
			return layer
		}
	}
	return memory.Scope{}
}
