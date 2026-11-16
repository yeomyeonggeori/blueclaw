package persona

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

type registerField struct {
	Enum        []string `json:"enum"`
	Description string   `json:"description"`
}

func registerFieldOf(t *testing.T, schemaDocument []byte) registerField {
	t.Helper()
	var schema struct {
		Properties struct {
			Tone struct {
				Properties struct {
					Register registerField `json:"register"`
				} `json:"properties"`
			} `json:"tone"`
		} `json:"properties"`
	}
	if errorValue := json.Unmarshal(schemaDocument, &schema); errorValue != nil {
		t.Fatalf("expected the schema to parse: %v", errorValue)
	}
	return schema.Properties.Tone.Properties.Register
}

func registerValues() []string {
	values := make([]string, 0, len(Registers))
	for _, register := range Registers {
		values = append(values, register.Value)
	}
	return values
}

func TestEveryRegisterTheSchemasAcceptHasBothLanguagesWritten(t *testing.T) {
	for name, schemaDocument := range map[string][]byte{"soul": SoulSchemaDocument, "user": UserSchemaDocument} {
		field := registerFieldOf(t, schemaDocument)
		if !slices.Equal(field.Enum, registerValues()) {
			t.Fatalf("expected the %s schema to accept exactly the registers in registers.json %v, got %v", name, registerValues(), field.Enum)
		}
		for _, register := range Registers {
			if !strings.Contains(field.Description, register.Value+" is "+register.Korean.Romanized) {
				t.Fatalf("expected the %s schema's register description to say %s is %s, got %q", name, register.Value, register.Korean.Romanized, field.Description)
			}
		}
	}
	for _, register := range Registers {
		for _, text := range []string{register.Korean.Name, register.Korean.Romanized, register.Korean.Example, register.English.Name, register.English.Style, register.English.Example} {
			if strings.TrimSpace(text) == "" {
				t.Fatalf("expected %s to have every name, style and example written, got %+v", register.Value, register)
			}
		}
	}
}

func TestThePlainRegisterIsWorded(t *testing.T) {
	soul, errorValue := ParseSoul([]byte(`{"schemaVersion": 1, "tone": {"register": "plain"}}`))
	if errorValue != nil {
		t.Fatalf("expected the plain register to be accepted: %v", errorValue)
	}
	expected := `Tone: in Korean use 해라체 ("확인했다. 내일 다시 알린다."); in English write terse notes and drop the subject where it reads naturally ("Confirmed. Will update tomorrow.").`
	if instruction := RenderSoulInstruction(soul); instruction != expected {
		t.Fatalf("expected %q, got %q", expected, instruction)
	}
}
