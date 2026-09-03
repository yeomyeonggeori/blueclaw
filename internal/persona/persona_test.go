package persona

import (
	"path/filepath"
	"strings"
	"testing"
)

const identityDocument = `{
  "schemaVersion": 1,
  "names": ["김인턴", "Intern Kim", " 인턴킴 "],
  "handle": "internkim",
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

const userDocument = `{
  "schemaVersion": 1,
  "callMe": "샘플님",
  "about": "플랫폼 팀에서 결제 서버를 맡고 있다.",
  "preferences": ["Give me the command before the explanation.", " Never send a message on my behalf without showing it first. "],
  "tone": {"register": "casual"},
  "language": {"default": "en"}
}`

func TestParseIdentityNormalizesAndRenders(t *testing.T) {
	identity, errorValue := ParseIdentity([]byte(identityDocument))
	if errorValue != nil {
		t.Fatalf("expected the identity to parse: %v", errorValue)
	}
	if len(identity.Names) != 3 || identity.Names[2] != "인턴킴" {
		t.Fatalf("expected trimmed names, got %v", identity.Names)
	}
	agentIdentity := AgentIdentityOf(identity)
	if agentIdentity.Name != "김인턴" || agentIdentity.Handle != "internkim" {
		t.Fatalf("expected the first name and the handle, got %+v", agentIdentity)
	}
	instruction := RenderIdentityInstruction(identity)
	for _, fragment := range []string{"Your name is 김인턴.", "all your name too, and you answer to any of them: \"Intern Kim\", \"인턴킴\".", "@internkim", "Your role: 회사의 AI 인턴", "Your emoji: 🐱", "introduce yourself like this: 안녕하세요, 김인턴입니다."} {
		if !strings.Contains(instruction, fragment) {
			t.Fatalf("expected the identity instruction to contain %q, got %q", fragment, instruction)
		}
	}
}

func TestAnIdentityWithoutAHandleStillHasAName(t *testing.T) {
	identity, errorValue := ParseIdentity([]byte(`{"schemaVersion": 1, "names": ["김인턴"]}`))
	if errorValue != nil {
		t.Fatalf("expected a handle-less identity to parse: %v", errorValue)
	}
	if AgentIdentityOf(identity).Name != "김인턴" || AgentIdentityOf(identity).Handle != "" {
		t.Fatalf("expected the name without a handle, got %+v", AgentIdentityOf(identity))
	}
	if strings.Contains(RenderIdentityInstruction(identity), "mention you as") {
		t.Fatal("expected no mention line without a handle")
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

func TestParseUserNormalizesAndRenders(t *testing.T) {
	user, errorValue := ParseUser([]byte(userDocument))
	if errorValue != nil {
		t.Fatalf("expected the user document to parse: %v", errorValue)
	}
	if len(user.Preferences) != 2 || strings.HasPrefix(user.Preferences[1], " ") {
		t.Fatalf("expected trimmed preferences, got %v", user.Preferences)
	}
	instruction := RenderUserInstruction(user)
	for _, fragment := range []string{"The person asking has told you how they want to work with you:", "- Call them 샘플님.", "- About them: 플랫폼 팀에서", "  - Give me the command before the explanation.", "- Tone with them: keep a casual register.", "- Answer them in en."} {
		if !strings.Contains(instruction, fragment) {
			t.Fatalf("expected the user instruction to contain %q, got %q", fragment, instruction)
		}
	}
	if RenderUserInstruction(User{}) != "" {
		t.Fatal("expected an empty user document to render nothing")
	}
}

func TestUserDocumentPathAcceptsOnlyAPlainPersonID(t *testing.T) {
	path, isValid := UserDocumentPath("/workspace", "person-1")
	if !isValid || path != filepath.Join("/workspace", ".blueclaw", "persona", "users", "person-1.json") {
		t.Fatalf("expected the user document under the service-owned directory, got %q %v", path, isValid)
	}
	for _, personID := range []string{"", "../etc", "a/b", ".hidden", "with space"} {
		if _, isValid := UserDocumentPath("/workspace", personID); isValid {
			t.Fatalf("expected %q to be refused as a person ID", personID)
		}
	}
}

func TestParseRefusesWhatTheSchemaDoesNotName(t *testing.T) {
	for name, document := range map[string]string{
		"an unknown identity field":    `{"schemaVersion": 1, "names": ["김인턴"], "nickname": "kim"}`,
		"no names":                     `{"schemaVersion": 1, "handle": "internkim"}`,
		"an empty names list":          `{"schemaVersion": 1, "names": []}`,
		"an uppercase handle":          `{"schemaVersion": 1, "names": ["김인턴"], "handle": "InternKim"}`,
		"another schema version":       `{"schemaVersion": 2, "names": ["김인턴"]}`,
		"too many names":               `{"schemaVersion": 1, "names": ["a","b","c","d","e","f","g","h","i"]}`,
		"an introduction that rambles": `{"schemaVersion": 1, "names": ["김인턴"], "introduction": "` + strings.Repeat("x", 401) + `"}`,
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
	for name, document := range map[string]string{
		"an unknown user field":      `{"schemaVersion": 1, "nickname": "kim"}`,
		"a matchRequester on a user": `{"schemaVersion": 1, "language": {"default": "ko", "matchRequester": true}}`,
		"too many preferences":       `{"schemaVersion": 1, "preferences": ["a","b","c","d","e","f","g","h","i"]}`,
	} {
		if _, errorValue := ParseUser([]byte(document)); errorValue == nil {
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
	user, errorValue := ParseUser([]byte(userDocument))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := CanonicalUser(user); errorValue != nil {
		t.Fatalf("expected a canonical user document: %v", errorValue)
	}
}
