package persona

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	IdentityFileName         = "identity.json"
	SoulFileName             = "soul.json"
	UserDocumentRelativePath = ".internkim/user.json"
	SchemaVersion            = 1
)

//go:embed schema/identity.schema.json
var IdentitySchemaDocument []byte

//go:embed schema/soul.schema.json
var SoulSchemaDocument []byte

//go:embed schema/user.schema.json
var UserSchemaDocument []byte

type Identity struct {
	SchemaVersion int      `json:"schemaVersion"`
	Names         []string `json:"names"`
	Handle        string   `json:"handle,omitempty"`
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

type User struct {
	SchemaVersion int           `json:"schemaVersion"`
	CallMe        string        `json:"callMe,omitempty"`
	About         string        `json:"about,omitempty"`
	Preferences   []string      `json:"preferences,omitempty"`
	Tone          *Tone         `json:"tone,omitempty"`
	Language      *UserLanguage `json:"language,omitempty"`
}

type Tone struct {
	Register string   `json:"register,omitempty"`
	Traits   []string `json:"traits,omitempty"`
}

type Language struct {
	Default        string `json:"default,omitempty"`
	MatchRequester bool   `json:"matchRequester,omitempty"`
}

type UserLanguage struct {
	Default string `json:"default,omitempty"`
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

func ParseUser(document []byte) (User, error) {
	if errorValue := validateAgainstSchema(UserSchemaDocument, "user.json", document); errorValue != nil {
		return User{}, errorValue
	}
	var user User
	if errorValue := json.Unmarshal(document, &user); errorValue != nil {
		return User{}, fmt.Errorf("user.json: %w", errorValue)
	}
	return NormalizeUser(user), nil
}

func NormalizeIdentity(identity Identity) Identity {
	identity.SchemaVersion = SchemaVersion
	identity.Names = normalizeLines(identity.Names)
	identity.Handle = strings.ToLower(strings.TrimSpace(identity.Handle))
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
	soul.Tone = normalizeTone(soul.Tone)
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

func NormalizeUser(user User) User {
	user.SchemaVersion = SchemaVersion
	user.CallMe = strings.TrimSpace(user.CallMe)
	user.About = strings.TrimSpace(user.About)
	user.Preferences = normalizeLines(user.Preferences)
	user.Tone = normalizeTone(user.Tone)
	if user.Language != nil {
		language := UserLanguage{Default: strings.TrimSpace(user.Language.Default)}
		if language.Default == "" {
			user.Language = nil
		} else {
			user.Language = &language
		}
	}
	return user
}

func normalizeTone(tone *Tone) *Tone {
	if tone == nil {
		return nil
	}
	normalized := Tone{Register: strings.ToLower(strings.TrimSpace(tone.Register)), Traits: normalizeLines(tone.Traits)}
	if normalized.Register == "" && len(normalized.Traits) == 0 {
		return nil
	}
	return &normalized
}

func CanonicalIdentity(identity Identity) ([]byte, error) {
	return canonicalDocument(NormalizeIdentity(identity), IdentitySchemaDocument, IdentityFileName)
}

func CanonicalSoul(soul Soul) ([]byte, error) {
	return canonicalDocument(NormalizeSoul(soul), SoulSchemaDocument, SoulFileName)
}

func CanonicalUser(user User) ([]byte, error) {
	return canonicalDocument(NormalizeUser(user), UserSchemaDocument, "user.json")
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
