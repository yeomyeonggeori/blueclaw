package persona

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	IdentityFileName = "identity.json"
	SoulFileName     = "soul.json"
	SchemaVersion    = 1
)

//go:embed schema/identity.schema.json
var IdentitySchemaDocument []byte

//go:embed schema/soul.schema.json
var SoulSchemaDocument []byte

type Identity struct {
	SchemaVersion int      `json:"schemaVersion"`
	Name          string   `json:"name"`
	EnglishName   string   `json:"englishName,omitempty"`
	Handle        string   `json:"handle"`
	Aliases       []string `json:"aliases,omitempty"`
	Role          string   `json:"role,omitempty"`
	Creature      string   `json:"creature,omitempty"`
	Emoji         string   `json:"emoji,omitempty"`
	Introduction  string   `json:"introduction,omitempty"`
}

type Soul struct {
	SchemaVersion int       `json:"schemaVersion"`
	Values        []string  `json:"values,omitempty"`
	Boundaries    []string  `json:"boundaries,omitempty"`
	WorkingStyle  []string  `json:"workingStyle,omitempty"`
	Tone          *Tone     `json:"tone,omitempty"`
	Language      *Language `json:"language,omitempty"`
}

type Tone struct {
	Register string   `json:"register,omitempty"`
	Traits   []string `json:"traits,omitempty"`
}

type Language struct {
	Default        string `json:"default,omitempty"`
	MatchRequester bool   `json:"matchRequester,omitempty"`
}

func ParseIdentity(document []byte) (Identity, error) {
	if errorValue := validateAgainstSchema(IdentitySchemaDocument, IdentityFileName, document); errorValue != nil {
		return Identity{}, errorValue
	}
	var identity Identity
	if errorValue := json.Unmarshal(document, &identity); errorValue != nil {
		return Identity{}, fmt.Errorf("%s: %w", IdentityFileName, errorValue)
	}
	return NormalizeIdentity(identity), nil
}

func ParseSoul(document []byte) (Soul, error) {
	if errorValue := validateAgainstSchema(SoulSchemaDocument, SoulFileName, document); errorValue != nil {
		return Soul{}, errorValue
	}
	var soul Soul
	if errorValue := json.Unmarshal(document, &soul); errorValue != nil {
		return Soul{}, fmt.Errorf("%s: %w", SoulFileName, errorValue)
	}
	return NormalizeSoul(soul), nil
}

func NormalizeIdentity(identity Identity) Identity {
	identity.SchemaVersion = SchemaVersion
	identity.Name = strings.TrimSpace(identity.Name)
	identity.EnglishName = strings.TrimSpace(identity.EnglishName)
	identity.Handle = strings.ToLower(strings.TrimSpace(identity.Handle))
	identity.Aliases = normalizeLines(identity.Aliases)
	identity.Role = strings.TrimSpace(identity.Role)
	identity.Creature = strings.TrimSpace(identity.Creature)
	identity.Emoji = strings.TrimSpace(identity.Emoji)
	identity.Introduction = strings.TrimSpace(identity.Introduction)
	return identity
}

func NormalizeSoul(soul Soul) Soul {
	soul.SchemaVersion = SchemaVersion
	soul.Values = normalizeLines(soul.Values)
	soul.Boundaries = normalizeLines(soul.Boundaries)
	soul.WorkingStyle = normalizeLines(soul.WorkingStyle)
	if soul.Tone != nil {
		tone := Tone{Register: strings.ToLower(strings.TrimSpace(soul.Tone.Register)), Traits: normalizeLines(soul.Tone.Traits)}
		if tone.Register == "" && len(tone.Traits) == 0 {
			soul.Tone = nil
		} else {
			soul.Tone = &tone
		}
	}
	if soul.Language != nil {
		language := Language{Default: strings.TrimSpace(soul.Language.Default), MatchRequester: soul.Language.MatchRequester}
		if language.Default == "" && !language.MatchRequester {
			soul.Language = nil
		} else {
			soul.Language = &language
		}
	}
	return soul
}

func CanonicalIdentity(identity Identity) ([]byte, error) {
	return canonicalDocument(NormalizeIdentity(identity), IdentitySchemaDocument, IdentityFileName)
}

func CanonicalSoul(soul Soul) ([]byte, error) {
	return canonicalDocument(NormalizeSoul(soul), SoulSchemaDocument, SoulFileName)
}

func canonicalDocument(value any, schemaDocument []byte, fileName string) ([]byte, error) {
	document, errorValue := json.MarshalIndent(value, "", "  ")
	if errorValue != nil {
		return nil, errorValue
	}
	document = append(document, '\n')
	if errorValue := validateAgainstSchema(schemaDocument, fileName, document); errorValue != nil {
		return nil, errorValue
	}
	return document, nil
}

func validateAgainstSchema(schemaDocument []byte, fileName string, document []byte) error {
	var schema jsonschema.Schema
	if errorValue := json.Unmarshal(schemaDocument, &schema); errorValue != nil {
		return fmt.Errorf("%s schema: %w", fileName, errorValue)
	}
	resolved, errorValue := schema.Resolve(nil)
	if errorValue != nil {
		return fmt.Errorf("%s schema: %w", fileName, errorValue)
	}
	var instance any
	if errorValue := json.Unmarshal(document, &instance); errorValue != nil {
		return fmt.Errorf("%s: %w", fileName, errorValue)
	}
	if errorValue := resolved.Validate(instance); errorValue != nil {
		return fmt.Errorf("%s: %w", fileName, errorValue)
	}
	return nil
}

func normalizeLines(lines []string) []string {
	seen := map[string]bool{}
	normalized := []string{}
	for _, line := range lines {
		trimmed := strings.Join(strings.Fields(line), " ")
		key := strings.ToLower(trimmed)
		if trimmed == "" || seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}
