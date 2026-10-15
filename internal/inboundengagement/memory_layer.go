package inboundengagement

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

// MemoryLayer is a memory read by exactly the people in a conversation, so
// keeping what they said there tells nobody anything they did not hear.
type MemoryLayer struct {
	Key  string
	Kind MemoryLayerKind
	Name string
}

type MemoryLayerKind string

const (
	MemoryLayerCompany MemoryLayerKind = "company"
	MemoryLayerCircle  MemoryLayerKind = "circle"
	MemoryLayerPerson  MemoryLayerKind = "person"
)

const (
	QuestionRemember   = "remember"
	RememberOptionNone = "none"
)

func asksRemember(facts Facts) bool {
	return len(facts.MemoryLayers) > 0
}

func rememberQuestion(about string, agentName string, layers []MemoryLayer) model.DecisionQuestion {
	descriptions := map[string]string{
		RememberOptionNone: "nothing lasting to keep: a question, a greeting, chatter, an opinion, one-off coordination, a request that states no lasting fact, or a fact the message only implies",
	}
	for _, layer := range layers {
		descriptions[layer.Key] = memoryLayerDescription(layer, agentName)
	}
	return model.ChoiceQuestion{
		Instructions: about + "Does it state something " + agentName + " should remember for the people in this conversation, whether or not it needs a reply or work? " +
			"Each option other than none is a memory read by exactly the people in this conversation. " +
			"Remember only a lasting fact the message itself states: a decision, a rule or convention, a standing arrangement, a preference, or a fact about a person, a team, or the company that stays true beyond this conversation. " +
			"Do not infer a fact the message does not state; its topic, tone, or wording alone is never a reason to remember it.",
		OptionDescriptions: descriptions,
	}.Question()
}

func memoryLayerDescription(layer MemoryLayer, agentName string) string {
	switch layer.Kind {
	case MemoryLayerCompany:
		return "a lasting fact everyone at the company should keep knowing, kept in the company's memory"
	case MemoryLayerCircle:
		return "a lasting fact the " + layer.Name + " circle should keep knowing, kept in that circle's memory"
	}
	return "a lasting fact about " + firstNonEmpty(layer.Name, "the person writing") + " that " + agentName + " should keep knowing for them, kept in their own memory"
}

// readRememberLayer keeps a message when the layers together outweigh none,
// in the likeliest of them, the way work is read: options that share a verdict
// are summed before they are compared with the one that refuses it.
func readRememberLayer(answer model.DecisionAnswer, layers []MemoryLayer) string {
	if len(answer.Probabilities) == 0 {
		return offeredLayerKey(strings.TrimSpace(answer.Choice), layers)
	}
	likeliestKey, likeliestWeight, keepingWeight := "", 0.0, 0.0
	for _, layer := range layers {
		weight := answer.ChoiceProbability(layer.Key)
		keepingWeight += weight
		if weight > likeliestWeight {
			likeliestKey, likeliestWeight = layer.Key, weight
		}
	}
	if keepingWeight <= answer.ChoiceProbability(RememberOptionNone) {
		return ""
	}
	return likeliestKey
}

func offeredLayerKey(key string, layers []MemoryLayer) string {
	for _, layer := range layers {
		if layer.Key == key {
			return key
		}
	}
	return ""
}
