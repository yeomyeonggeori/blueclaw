package turnbriefing

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func Preamble(request agentcontract.AgentTurnRequest, instructionPrompt string) string {
	sections := []string{}
	if standingInstructions := strings.TrimSpace(instructionPrompt); standingInstructions != "" {
		sections = append(sections, standingInstructions)
	}
	if introduction := agentIntroduction(request); introduction != "" {
		sections = append(sections, introduction)
	}
	if temporalContext := agentcontract.BuildTemporalContextDescription(request.EnvironmentNow, request.Company.TimeZone); temporalContext != "" {
		sections = append(sections, temporalContext)
	}
	if facts := rememberedFacts(request.MemoryFacts); facts != "" {
		sections = append(sections, facts)
	}
	if carriedOut := callsAlreadyCarriedOut(request.CarriedOutCalls); carriedOut != "" {
		sections = append(sections, carriedOut)
	}
	return strings.Join(sections, "\n\n")
}

func callsAlreadyCarriedOut(carriedOutCalls []agentcontract.CarriedOutCall) string {
	lines := []string{}
	for _, carriedOutCall := range carriedOutCalls {
		toolName := strings.TrimSpace(carriedOutCall.ToolName)
		if toolName == "" {
			continue
		}
		lines = append(lines, "- "+toolName+" — "+carriedOutCallOutcome(carriedOutCall.Result))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Already carried out for you, with the requester's approval. Do not issue these calls again:\n" + strings.Join(lines, "\n")
}

func carriedOutCallOutcome(result toolcontract.ToolResult) string {
	if result.Failed() {
		return "failed: " + result.UserSafeFailureSummary()
	}
	if content := strings.TrimSpace(result.ContentText()); content != "" {
		return content
	}
	return "done"
}

func agentIntroduction(request agentcontract.AgentTurnRequest) string {
	lines := []string{}
	if agentName := strings.TrimSpace(request.AgentIdentity.Name); agentName != "" {
		introduction := "You are " + agentName
		if handle := strings.TrimSpace(request.AgentIdentity.Handle); handle != "" {
			introduction += " (" + handle + ")"
		}
		if company := strings.TrimSpace(request.Company.Name); company != "" {
			introduction += " at " + company
		}
		lines = append(lines, introduction+".")
	}
	if requesterName := strings.TrimSpace(request.RequesterName); requesterName != "" {
		lines = append(lines, "The person asking is "+requesterName+".")
	}
	if responseLanguage := strings.TrimSpace(request.ResponseLanguage); responseLanguage != "" {
		lines = append(lines, "Answer them in "+responseLanguage+".")
	}
	return strings.Join(lines, "\n")
}

func rememberedFacts(memoryFacts []agentcontract.MemoryFact) string {
	statements := []string{}
	for _, memoryFact := range memoryFacts {
		if statement := strings.TrimSpace(memoryFact.Content); statement != "" {
			statements = append(statements, "- "+statement)
		}
	}
	if len(statements) == 0 {
		return ""
	}
	return "What you already know:\n" + strings.Join(statements, "\n")
}
