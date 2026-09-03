package persona

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func AgentIdentityOf(identity Identity) agentcontract.AgentIdentity {
	return agentcontract.AgentIdentity{Name: identity.Name, Handle: identity.Handle}
}

func RenderIdentityInstruction(identity Identity) string {
	identity = NormalizeIdentity(identity)
	lines := []string{"Identity:"}
	lines = append(lines, "- Your name is "+identity.Name+nameSuffix(identity)+".")
	if identity.Handle != "" {
		lines = append(lines, "- People mention you as @"+identity.Handle+".")
	}
	if len(identity.Aliases) > 0 {
		lines = append(lines, "- People also write your name as "+joinQuoted(identity.Aliases)+".")
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
	if toneLine := renderTone(soul.Tone); toneLine != "" {
		sections = append(sections, toneLine)
	}
	if languageLine := renderLanguage(soul.Language); languageLine != "" {
		sections = append(sections, languageLine)
	}
	return strings.Join(sections, "\n")
}

func nameSuffix(identity Identity) string {
	if identity.EnglishName == "" || identity.EnglishName == identity.Name {
		return ""
	}
	return " (" + identity.EnglishName + ")"
}

func renderTone(tone *Tone) string {
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
	return "Tone: " + strings.Join(parts, "; ") + "."
}

func renderLanguage(language *Language) string {
	if language == nil {
		return ""
	}
	parts := []string{}
	if language.Default != "" {
		parts = append(parts, "write in "+language.Default+" unless something else decides it")
	}
	if language.MatchRequester {
		parts = append(parts, "answer in the language the requester wrote in")
	}
	return "Language: " + strings.Join(parts, "; ") + "."
}

func bulleted(lines []string) string {
	bullets := make([]string, 0, len(lines))
	for _, line := range lines {
		bullets = append(bullets, "- "+line)
	}
	return strings.Join(bullets, "\n")
}

func joinQuoted(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "\""+value+"\"")
	}
	return strings.Join(quoted, ", ")
}
