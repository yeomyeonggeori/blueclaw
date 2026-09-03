package persona

import (
	"strings"
	"testing"
)

const identityDocument = `{
  "schemaVersion": 1,
  "name": "김인턴",
  "englishName": "Intern Kim",
  "handle": "internkim",
  "aliases": ["인턴킴", " intern kim "],
  "role": "회사의 AI 인턴",
  "creature": "흰색 고양이 마스코트",
  "emoji": "🐱",
  "introduction": "안녕하세요, 김인턴입니다."
}`

const soulDocument = `{
  "schemaVersion": 1,
  "values": ["Lead with the result.", "Say what you did not do."],
  "boundaries": ["Never read another person's direct messages."],
  "workingStyle": ["Ask once, then act."],
  "tone": {"register": "polite", "traits": ["warm", "brief"]},
  "language": {"default": "ko", "matchRequester": true}
}`

func TestParseIdentityNormalizesAndRenders(t *testing.T) {
	identity, errorValue := ParseIdentity([]byte(identityDocument))
	if errorValue != nil {
		t.Fatalf("expected the identity to parse: %v", errorValue)
	}
	if len(identity.Aliases) != 2 || identity.Aliases[1] != "intern kim" {
		t.Fatalf("expected trimmed aliases, got %v", identity.Aliases)
	}
	if deduplicated := NormalizeIdentity(Identity{Aliases: []string{"인턴킴", "인턴킴", " Intern Kim "}}).Aliases; len(deduplicated) != 2 {
		t.Fatalf("expected normalization to drop a repeated alias, got %v", deduplicated)
	}
	agentIdentity := AgentIdentityOf(identity)
	if agentIdentity.Name != "김인턴" || agentIdentity.Handle != "internkim" {
		t.Fatalf("expected the agent identity to carry name and handle, got %+v", agentIdentity)
	}
	instruction := RenderIdentityInstruction(identity)
	for _, fragment := range []string{"Your name is 김인턴 (Intern Kim).", "@internkim", "\"인턴킴\", \"intern kim\"", "Your role: 회사의 AI 인턴", "Your emoji: 🐱", "introduce yourself like this: 안녕하세요, 김인턴입니다."} {
		if !strings.Contains(instruction, fragment) {
			t.Fatalf("expected the identity instruction to contain %q, got %q", fragment, instruction)
		}
	}
}

func TestParseSoulNormalizesAndRenders(t *testing.T) {
	soul, errorValue := ParseSoul([]byte(soulDocument))
	if errorValue != nil {
		t.Fatalf("expected the soul to parse: %v", errorValue)
	}
	instruction := RenderSoulInstruction(soul)
	for _, fragment := range []string{"What you hold to:\n- Lead with the result.\n- Say what you did not do.", "What you never do:\n- Never read", "How you work:\n- Ask once", "Tone: keep a polite register; sound warm, brief.", "Language: write in ko unless something else decides it; answer in the language the requester wrote in."} {
		if !strings.Contains(instruction, fragment) {
			t.Fatalf("expected the soul instruction to contain %q, got %q", fragment, instruction)
		}
	}
}

func TestParseRefusesWhatTheSchemaDoesNotName(t *testing.T) {
	for name, document := range map[string]string{
		"an unknown identity field":    `{"schemaVersion": 1, "name": "김인턴", "handle": "internkim", "nickname": "kim"}`,
		"a missing name":               `{"schemaVersion": 1, "handle": "internkim"}`,
		"an uppercase handle":          `{"schemaVersion": 1, "name": "김인턴", "handle": "InternKim"}`,
		"another schema version":       `{"schemaVersion": 2, "name": "김인턴", "handle": "internkim"}`,
		"too many aliases":             `{"schemaVersion": 1, "name": "김인턴", "handle": "internkim", "aliases": ["a","b","c","d","e","f","g","h","i"]}`,
		"an introduction that rambles": `{"schemaVersion": 1, "name": "김인턴", "handle": "internkim", "introduction": "` + strings.Repeat("x", 401) + `"}`,
	} {
		if _, errorValue := ParseIdentity([]byte(document)); errorValue == nil {
			t.Fatalf("expected %s to be refused", name)
		}
	}
	for name, document := range map[string]string{
		"an unknown soul field": `{"schemaVersion": 1, "mood": "happy"}`,
		"an unknown register":   `{"schemaVersion": 1, "tone": {"register": "shouty"}}`,
		"a malformed language":  `{"schemaVersion": 1, "language": {"default": "Korean"}}`,
		"a duplicated value":    `{"schemaVersion": 1, "values": ["Lead with the result.", "Lead with the result."]}`,
	} {
		if _, errorValue := ParseSoul([]byte(document)); errorValue == nil {
			t.Fatalf("expected %s to be refused", name)
		}
	}
}

func TestCanonicalDocumentsRoundTrip(t *testing.T) {
	identity, errorValue := ParseIdentity([]byte(identityDocument))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	canonical, errorValue := CanonicalIdentity(identity)
	if errorValue != nil {
		t.Fatalf("expected a canonical identity: %v", errorValue)
	}
	reparsed, errorValue := ParseIdentity(canonical)
	if errorValue != nil {
		t.Fatalf("expected the canonical identity to parse: %v", errorValue)
	}
	second, _ := CanonicalIdentity(reparsed)
	if string(second) != string(canonical) {
		t.Fatalf("expected a stable canonical form, got\n%s\nand\n%s", canonical, second)
	}
	soul, errorValue := ParseSoul([]byte(soulDocument))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := CanonicalSoul(soul); errorValue != nil {
		t.Fatalf("expected a canonical soul: %v", errorValue)
	}
	emptySoul, errorValue := CanonicalSoul(Soul{Tone: &Tone{}, Language: &Language{}})
	if errorValue != nil || strings.Contains(string(emptySoul), "tone") {
		t.Fatalf("expected empty tone and language to be dropped, got %s (%v)", emptySoul, errorValue)
	}
}
