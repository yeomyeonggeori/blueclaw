package persona

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed schema/registers.json
var RegistersDocument []byte

type Register struct {
	Value   string          `json:"value"`
	Korean  KoreanRegister  `json:"korean"`
	English EnglishRegister `json:"english"`
}

type KoreanRegister struct {
	Name      string `json:"name"`
	Romanized string `json:"romanized"`
	Example   string `json:"example"`
}

type EnglishRegister struct {
	Name    string `json:"name"`
	Style   string `json:"style"`
	Example string `json:"example"`
}

var Registers = readRegisters()

func readRegisters() []Register {
	var document struct {
		Registers []Register `json:"registers"`
	}
	if errorValue := json.Unmarshal(RegistersDocument, &document); errorValue != nil {
		panic(errorValue)
	}
	return document.Registers
}

func registerOf(value string) (Register, bool) {
	for _, register := range Registers {
		if register.Value == value {
			return register, true
		}
	}
	return Register{}, false
}

func registerInstruction(value string) string {
	register, found := registerOf(value)
	if !found {
		return ""
	}
	return fmt.Sprintf("in Korean use %s (%q); in English %s (%q)", register.Korean.Name, register.Korean.Example, register.English.Style, register.English.Example)
}
