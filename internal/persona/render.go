package persona

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func AgentIdentityOf(identity Identity) agentcontract.AgentIdentity {
	identity = NormalizeIdentity(identity)
	agentIdentity := agentcontract.AgentIdentity{Handle: identity.Handle}
	if len(identity.Names) > 0 {
		agentIdentity.Name = identity.Names[0]
	}
	return agentIdentity
}

func RenderIdentityInstruction(identity Identity) string {
	identity = NormalizeIdentity(identity)
	lines := []string{"Identity:"}
	if len(identity.Names) > 0 {
		lines = append(lines, "- Your name is "+identity.Names[0]+".")
	}
	if len(identity.Names) > 1 {
		lines = append(lines, "- These are all your name too, and you answer to any of them: "+joinQuoted(identity.Names[1:])+".")
	}
	if identity.Handle != "" {
		lines = append(lines, "- People mention you as @"+identity.Handle+".")
	}
	if identity.Role != "" {
		lines = append(lines, "- Your role: "+identity.Role)
	}
	if identity.Creature != "" {
		lines = append(lines, "- What you are: "+identity.Creature)
	}
	if identity.Emoji != "" {
		lines = append(lines, "- Your emoji: "+identity.Emoji)
	}
	if identity.Introduction != "" {
		lines = append(lines, "- When asked who you are, introduce yourself like this: "+identity.Introduction)
	}
	return strings.Join(lines, "\n")
}

func RenderSoulInstruction(soul Soul) string {
	soul = NormalizeSoul(soul)
	sections := []string{}
	if len(soul.Values) > 0 {
		sections = append(sections, "What you hold to:\n"+bulleted(soul.Values))
	}
	if len(soul.Boundaries) > 0 {
		sections = append(sections, "What you never do:\n"+bulleted(soul.Boundaries))
	}
	if len(soul.WorkingStyle) > 0 {
		sections = append(sections, "How you work:\n"+bulleted(soul.WorkingStyle))
	}
	if toneLine := renderTone("Tone", soul.Tone); toneLine != "" {
		sections = append(sections, toneLine)
	}
	if soul.Language != nil {
		parts := []string{}
		if soul.Language.Default != "" {
			parts = append(parts, "write in "+soul.Language.Default+" unless something else decides it")
		}
		if soul.Language.MatchRequester {
			parts = append(parts, "answer in the language the requester wrote in")
		}
		if len(parts) > 0 {
			sections = append(sections, "Language: "+strings.Join(parts, "; ")+".")
		}
	}
	return strings.Join(sections, "\n")
}

func RenderUserInstruction(user User) string {
	user = NormalizeUser(user)
	lines := []string{}
	if user.CallMe != "" {
		lines = append(lines, "- Call them "+user.CallMe+".")
	}
	if user.About != "" {
		lines = append(lines, "- About them: "+user.About)
	}
	if len(user.Preferences) > 0 {
		lines = append(lines, "- How they want you to work with them:\n"+indented(bulleted(user.Preferences)))
	}
	if toneLine := renderTone("Tone with them", user.Tone); toneLine != "" {
		lines = append(lines, "- "+toneLine)
	}
	if user.Language != nil && user.Language.Default != "" {
		lines = append(lines, "- Answer them in "+user.Language.Default+".")
	}
	if len(lines) == 0 {
		return ""
	}
	return "The person asking has told you how they want to work with you:\n" + strings.Join(lines, "\n")
}

func renderTone(label string, tone *Tone) string {
	if tone == nil {
		return ""
	}
	parts := []string{}
	if tone.Register != "" {
		parts = append(parts, "keep a "+tone.Register+" register")
	}
	if len(tone.Traits) > 0 {
		parts = append(parts, "sound "+strings.Join(tone.Traits, ", "))
	}
	return label + ": " + strings.Join(parts, "; ") + "."
}

func bulleted(lines []string) string {
	bullets := make([]string, 0, len(lines))
	for _, line := range lines {
		bullets = append(bullets, "- "+line)
	}
	return strings.Join(bullets, "\n")
}

func indented(block string) string {
	return "  " + strings.ReplaceAll(block, "\n", "\n  ")
}

func joinQuoted(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "\""+value+"\"")
	}
	return strings.Join(quoted, ", ")
}
