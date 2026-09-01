package turnbriefing

import (
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func briefedRequest() agentcontract.AgentTurnRequest {
	return agentcontract.AgentTurnRequest{
		AgentIdentity:    agentcontract.AgentIdentity{Name: "인턴킴", Handle: "@internkim"},
		Company:          agentcontract.CompanyContext{Name: "여명거리"},
		RequesterName:    "Ada",
		ResponseLanguage: "ko",
		MemoryFacts:      []agentcontract.MemoryFact{{Content: "Ada는 금요일 오후에 회의를 잡지 않는다"}},
	}
}

func TestAnAgentIsToldWhoItIsAndWhoIsAsking(t *testing.T) {
	preamble := Preamble(briefedRequest(), "Answer from evidence you have gathered.")

	for _, expectedFragment := range []string{
		"Answer from evidence you have gathered.",
		"인턴킴",
		"@internkim",
		"여명거리",
		"Ada",
		"ko",
		"Ada는 금요일 오후에 회의를 잡지 않는다",
	} {
		if !strings.Contains(preamble, expectedFragment) {
			t.Fatalf("an agent that is told none of this answers as nobody, expected %q in:\n%s", expectedFragment, preamble)
		}
	}
}

func TestAnAgentIsToldWhichCallTheHostAlreadyCarriedOut(t *testing.T) {
	request := briefedRequest()
	request.CarriedOutCalls = []agentcontract.CarriedOutCall{{
		ToolName: "event_delete",
		Result:   toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: "내일 10시 회의를 삭제했습니다"}},
	}}

	preamble := Preamble(request, "")

	for _, expectedFragment := range []string{"event_delete", "내일 10시 회의를 삭제했습니다", "Do not issue these calls again"} {
		if !strings.Contains(preamble, expectedFragment) {
			t.Fatalf("an agent that is not told this issues the approved call a second time, expected %q in:\n%s", expectedFragment, preamble)
		}
	}
}

func TestACarriedOutCallThatFailedIsReportedAsFailed(t *testing.T) {
	request := briefedRequest()
	request.CarriedOutCalls = []agentcontract.CarriedOutCall{{
		ToolName: "message_send",
		Result:   toolcontract.ToolFailureResult(toolcontract.FailureExternalService, "operation_failed", "connector", "메신저가 응답하지 않았습니다"),
	}}

	preamble := Preamble(request, "")

	if !strings.Contains(preamble, "failed") || !strings.Contains(preamble, "메신저가 응답하지 않았습니다") {
		t.Fatalf("expected the agent to learn the approved call did not go through, got:\n%s", preamble)
	}
}

func TestATurnWithNothingToSayCarriesNoPreamble(t *testing.T) {
	if preamble := Preamble(agentcontract.AgentTurnRequest{}, ""); preamble != "" {
		t.Fatalf("expected no preamble when there is nothing to brief, got %q", preamble)
	}
}

func TestAnAgentIsToldWhatDayItIs(t *testing.T) {
	request := briefedRequest()
	request.EnvironmentNow = time.Date(2026, time.September, 1, 12, 31, 0, 0, time.UTC)

	preamble := Preamble(request, "")

	for _, expectedFragment := range []string{"Now:", "2026-09-01", "This week:"} {
		if !strings.Contains(preamble, expectedFragment) {
			t.Fatalf("an agent that is not told the date reads the newest message in the conversation as today, expected %q in:\n%s", expectedFragment, preamble)
		}
	}
}
