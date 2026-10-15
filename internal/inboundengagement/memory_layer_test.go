package inboundengagement

import (
	"context"
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

func leadershipLayer() MemoryLayer {
	return MemoryLayer{Key: "circle:leadership", Kind: MemoryLayerCircle, Name: "leadership"}
}

func TestTheRememberQuestionIsAskedOnlyWhereALayerIsOffered(t *testing.T) {
	answers := defaultAnswers()
	answers.choices[QuestionRemember] = RememberOptionNone
	decider := NewDecisionModelDecider(answers, nil)
	if _, errorValue := decider.Decide(context.Background(), messageFacts("channel", true, "안녕하세요"), nil); errorValue != nil {
		t.Fatal(errorValue)
	}
	if slices.Contains(answers.askedQuestionNames(), "m1."+QuestionRemember) {
		t.Fatalf("expected no remember question without a layer, asked %v", answers.askedQuestionNames())
	}
	facts := messageFacts("channel", true, "다음 분기부터 임원회의는 화요일입니다")
	facts.MemoryLayers = []MemoryLayer{leadershipLayer()}
	if _, errorValue := decider.Decide(context.Background(), facts, nil); errorValue != nil {
		t.Fatal(errorValue)
	}
	request := answers.requests[len(answers.requests)-1]
	question, isAsked := request.Questions["m1."+QuestionRemember]
	if !isAsked {
		t.Fatalf("expected a remember question where a layer is offered, asked %v", answers.askedQuestionNames())
	}
	options, isChoice := question.Criteria.(map[string]string)
	if _, isOffered := options[leadershipLayer().Key]; !isChoice || !isOffered {
		t.Fatalf("expected the leadership layer among the options, got %+v", question.Criteria)
	}
}

func TestALayerIsKeptWhenTheLayersTogetherOutweighNone(t *testing.T) {
	layers := []MemoryLayer{leadershipLayer(), {Key: "circle:platform", Kind: MemoryLayerCircle, Name: "platform"}}
	split := model.DecisionAnswer{Choice: RememberOptionNone, Probabilities: map[string]float64{RememberOptionNone: 0.4, "circle:leadership": 0.35, "circle:platform": 0.25}}
	if key := readRememberLayer(split, layers); key != "circle:leadership" {
		t.Fatalf("expected the split verdict to keep it in the likeliest layer, got %q", key)
	}
	refused := model.DecisionAnswer{Choice: RememberOptionNone, Probabilities: map[string]float64{RememberOptionNone: 0.6, "circle:leadership": 0.4}}
	if key := readRememberLayer(refused, layers); key != "" {
		t.Fatalf("expected none to win, got %q", key)
	}
}

func TestALayerNotOfferedIsNeverKept(t *testing.T) {
	answer := model.DecisionAnswer{Choice: "company"}
	if key := readRememberLayer(answer, []MemoryLayer{leadershipLayer()}); key != "" {
		t.Fatalf("expected a layer the room was not offered to be dropped, got %q", key)
	}
}

func TestAMessageWorthOnlyRememberingLaunchesNothingAndChoosesNoReaction(t *testing.T) {
	judgment := Judgment{Addressing: AddressingDecision{Target: AddressingTargetAnyone, ReactionEmoji: "eyes"}, RememberLayer: "company"}
	decision := resolveJudgment(context.Background(), channelRequest(), judgment)
	if decision.ShouldLaunch || decision.RememberLayer != "company" || decision.ReactionEmoji != "" {
		t.Fatalf("expected a remember-only decision with no reaction of its own, got %+v", decision)
	}
}

func TestWorkThatIsAlsoWorthRememberingCarriesTheLayer(t *testing.T) {
	judgment := Judgment{Addressing: AddressingDecision{Target: AddressingTargetBot, ShouldRespond: true}, RememberLayer: "company"}
	decision := resolveJudgment(context.Background(), channelRequest(), judgment)
	if !decision.ShouldLaunch || decision.RememberLayer != "company" {
		t.Fatalf("expected a launch that also remembers, got %+v", decision)
	}
}
