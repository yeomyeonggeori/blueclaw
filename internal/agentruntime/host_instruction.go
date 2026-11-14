package agentruntime

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func hostInstructionForRequest(request agentcontract.AgentTurnRequest) string {
	instruction := "\n\nApprovals and user input: Ask the user only when their choice or free-form input is required. Never call ask.confirm. Invoke capability and file operations directly; the runtime evaluates structured approval policy and pauses before execution when approval is required. For bash, set approvalRequired=true with an approvalReason only when your judgment is that the exact command requires the user's confirmation; otherwise omit it or set it false. Deleting a workspace file the user named follows the runtime approval path: find the file yourself (ls ~/documents) and remove it with bash, setting approvalRequired=true. Ask with a reply that carries expectsAnswer=true and puts the question in message; the task pauses and the user's answer comes back as a new message. Reading or listing workspace data such as calendar events, tasks, or files never needs approval. If no user input is pending, send a final reply or proceed directly."
	instruction += " Put the exact question shown to the user in the reply message field, written in the requester's language — there is no separate userFacingMessage field."
	instruction += "\n\nRecipients: Send capabilities resolve recipients themselves. Pass the name the user gave, without honorific or particle, or a personID a tool result showed, as personHint, without searching memory or the web for contact details, and never ask the user for account IDs or handles. Do not search for, disambiguate, or invent the recipient yourself before invoking it. When a result returns candidates, ask the user which one with a reply carrying expectsAnswer=true, naming each candidate by name and address, then pass the chosen candidate's personID; never offer someone the result did not return. On recipient_not_found tell the user that name is not registered and ask only for the correct name."
	instruction += "\n\nPrivacy boundary applies to direct messages and channels, NOT to work tasks. A message read answers with what both the requester and you can open, and the messenger decides that on every call: it refuses a conversation either of you is not in and says which of you was refused. You are not in a direct conversation between two other people, the requester's with a colleague included, so that one is read by the requester themself and not through you. Being an admin changes none of this. Work tasks are shared, not private: you may look up anyone's tasks. task_list defaults to the requester's tasks. Set targetPersonHint for one specific person, or scope to all only when the user explicitly asks for workspace-wide tasks. Never tell the user you can only see their own tasks."
	return strings.TrimSpace(instruction)
}
