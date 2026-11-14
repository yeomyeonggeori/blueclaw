package turnbriefing

import (
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type TaskEventReader interface {
	ListTaskEvent(taskRunID string) []agentcontract.TaskEvent
}

type Briefing struct {
	InstructionPrompt         string
	TurnContextBriefedByAgent bool
	TaskEvents                TaskEventReader
}

func Prompt(request agentcontract.AgentTurnRequest, briefing Briefing) string {
	sections := []string{Preamble(request, briefing.InstructionPrompt), strings.TrimSpace(request.HostInstruction)}
	if !briefing.TurnContextBriefedByAgent {
		sections = append(sections, TurnContext(request))
	}
	sections = append(sections, request.Prompt, declinedCallNote(request, briefing.TaskEvents))
	return joinPresent(sections)
}

func Preamble(request agentcontract.AgentTurnRequest, instructionPrompt string) string {
	return joinPresent([]string{
		strings.TrimSpace(instructionPrompt),
		agentIntroduction(request),
		agentcontract.BuildTemporalContextDescription(request.EnvironmentNow, request.Company.TimeZone, request.VisibleContext.RequesterTimeZone),
		agentcontract.BuildMemoryContext(request.MemoryFacts),
		callsAlreadyCarriedOut(request.CarriedOutCalls),
	})
}

func TurnContext(request agentcontract.AgentTurnRequest) string {
	return joinPresent([]string{
		agentcontract.BuildVisibleContextDescription(request.VisibleContext, request.Company.TimeZone),
		agentcontract.ScheduledRunDescriptionForPrompt(request.ScheduledRun),
		agentcontract.ActiveGoalDescription(request.ActiveGoal, request.Prompt),
		agentcontract.PriorTaskDescription(request.PriorTask),
		agentcontract.PendingInputDescription(request.PendingInput),
		workspaceDescription(request.WorkspaceGuidance),
	})
}

func workspaceDescription(workspaceGuidance []string) string {
	lines := joinPresentLines(workspaceGuidance)
	if lines == "" {
		return ""
	}
	return "Workspace:\n" + lines
}

func declinedCallNote(request agentcontract.AgentTurnRequest, taskEvents TaskEventReader) string {
	if taskEvents == nil || strings.TrimSpace(request.ExistingTaskRunID) == "" {
		return ""
	}
	return approvalgate.DeclinedCallNote(taskEvents.ListTaskEvent(request.ExistingTaskRunID))
}

func joinPresent(sections []string) string {
	present := []string{}
	for _, section := range sections {
		if trimmed := strings.TrimSpace(section); trimmed != "" {
			present = append(present, trimmed)
		}
	}
	return strings.Join(present, "\n\n")
}

func joinPresentLines(lines []string) string {
	present := []string{}
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			present = append(present, trimmed)
		}
	}
	return strings.Join(present, "\n")
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
		lines = append(lines, "Write every reply and question to them in "+languageName(responseLanguage)+".")
	}
	return strings.Join(lines, "\n")
}

func languageName(responseLanguage string) string {
	if name := toolcontract.ResponseLanguageName(responseLanguage); name != "" {
		return name
	}
	return responseLanguage
}
