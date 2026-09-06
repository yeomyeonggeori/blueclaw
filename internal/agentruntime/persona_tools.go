package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type personaToolInput struct {
	Target string          `json:"target"`
	Patch  json.RawMessage `json:"patch,omitempty"`
}

var personaToolSchema = buildPersonaToolSchema()
var personaUserUpdateSchema = buildPersonaUserUpdateSchema()
var personaToolReadSchema = json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","enum":["user","soul"]}},"additionalProperties":false}`)
var personaToolIntentSchema = buildPersonaToolIntentSchema()
var personaToolOutputSchema = buildPersonaToolOutputSchema()

func registerPersonaTools(builder *ToolCatalogBuilder, registry *toolcontract.ToolSet, request ToolCatalogRequest) {
	toolcontract.RegisterToolFunction(registry, toolcontract.ToolFunction[personaToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{Name: "persona_read", Description: "Read the current requester profile or shared agent working guidance. Use this when deciding whether a stable preference belongs in user.json, shared working style belongs in soul.json, or a durable fact belongs in memory.", InputSchema: personaToolReadSchema},
		Handler: func(ctx context.Context, input personaToolInput) (toolcontract.ToolResult, error) {
			return builder.readPersonaTool(ctx, input, request)
		}, Result: toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(registry, toolcontract.ToolFunction[personaToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{Name: "persona_update", Description: "Update only the current requester's user profile with a validated patch. Omitted fields are preserved. Store durable facts and decisions in memory; shared working principles are managed internally.", InputSchema: personaUserUpdateSchema},
		Handler: func(ctx context.Context, input personaToolInput) (toolcontract.ToolResult, error) {
			return builder.updatePersonaTool(ctx, input, request)
		}, Result: toolcontract.IdentityToolResult,
	})
}

func (builder *ToolCatalogBuilder) readPersonaTool(ctx context.Context, input personaToolInput, request ToolCatalogRequest) (toolcontract.ToolResult, error) {
	document, errorValue := builder.personaDocument(ctx, input.Target, request)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "persona_read", errorValue.Error()), nil
	}
	return personaToolSuccess(input.Target, document), nil
}

func (builder *ToolCatalogBuilder) updatePersonaTool(ctx context.Context, input personaToolInput, request ToolCatalogRequest) (toolcontract.ToolResult, error) {
	target := strings.TrimSpace(input.Target)
	if target != "user" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "persona_update", "target must be user or soul"), nil
	}
	current, errorValue := builder.personaDocument(ctx, target, request)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "persona_update", errorValue.Error()), nil
	}
	canonical, errorValue := mergePersonaDocument(target, current, input.Patch)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "persona_update", errorValue.Error()), nil
	}
	if errorValue := builder.writePersonaDocument(ctx, target, canonical, request); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "persona_update", errorValue.Error()), nil
	}
	return personaToolSuccess(target, canonical), nil
}

func (builder *ToolCatalogBuilder) personaDocument(ctx context.Context, target string, request ToolCatalogRequest) ([]byte, error) {
	if target == "soul" {
		document, errorValue := persona.ReadSoulDocument(builder.workspaceRootPath)
		if errorValue != nil {
			return nil, errorValue
		}
		soul, errorValue := persona.ParseSoul(document)
		if errorValue != nil {
			return nil, errorValue
		}
		return persona.CanonicalSoul(soul)
	}
	if target != "user" || builder.workspaceActorFactory == nil {
		return nil, os.ErrPermission
	}
	personID := strings.TrimSpace(request.PersonAccess.PersonID)
	if personID == "" {
		return nil, os.ErrPermission
	}
	actor, errorValue := builder.workspaceActorFactory.Requester(ctx, security.WorkspaceActorRequest{PersonAccess: request.PersonAccess, WorkspaceRootPath: builder.workspaceRootPath})
	if errorValue != nil {
		return nil, errorValue
	}
	path := filepath.Join(security.PersonHomeDirectoryPath(builder.workspaceRootPath, personID), persona.UserDocumentRelativePath)
	document, errorValue := actor.ReadFile(ctx, path, 64*1024)
	if security.IsActorNotFoundError(errorValue) {
		return persona.CanonicalUser(persona.User{})
	}
	if errorValue != nil {
		return nil, errorValue
	}
	user, errorValue := persona.ParseUser(document)
	if errorValue != nil {
		return nil, errorValue
	}
	return persona.CanonicalUser(user)
}

func (builder *ToolCatalogBuilder) writePersonaDocument(ctx context.Context, target string, document []byte, request ToolCatalogRequest) error {
	if target == "soul" {
		return os.ErrPermission
	}
	actor, errorValue := builder.workspaceActorFactory.Requester(ctx, security.WorkspaceActorRequest{PersonAccess: request.PersonAccess, WorkspaceRootPath: builder.workspaceRootPath})
	if errorValue != nil {
		return errorValue
	}
	path := filepath.Join(security.PersonHomeDirectoryPath(builder.workspaceRootPath, request.PersonAccess.PersonID), persona.UserDocumentRelativePath)
	if errorValue := actor.MkdirAll(ctx, filepath.Dir(path)); errorValue != nil {
		return errorValue
	}
	if errorValue := actor.WriteFile(ctx, path, document); errorValue != nil {
		return errorValue
	}
	persona.SaveBackup(persona.UserBackupPath(builder.workspaceRootPath, request.PersonAccess.PersonID), document)
	return nil
}

func mergePersonaDocument(target string, current []byte, patch []byte) ([]byte, error) {
	if len(patch) == 0 {
		patch = []byte(`{}`)
	}
	var base, changes map[string]json.RawMessage
	if err := json.Unmarshal(current, &base); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(patch, &changes); err != nil {
		return nil, err
	}
	mergePersonaFields(base, changes)
	merged, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	if target == "user" {
		user, err := persona.ParseUser(merged)
		if err != nil {
			return nil, err
		}
		return persona.CanonicalUser(user)
	}
	soul, err := persona.ParseSoul(merged)
	if err != nil {
		return nil, err
	}
	return persona.CanonicalSoul(soul)
}

func mergePersonaFields(base map[string]json.RawMessage, changes map[string]json.RawMessage) {
	for key, value := range changes {
		var currentObject, changedObject map[string]json.RawMessage
		if json.Unmarshal(base[key], &currentObject) == nil && json.Unmarshal(value, &changedObject) == nil && currentObject != nil && changedObject != nil {
			mergePersonaFields(currentObject, changedObject)
			mergedObject, _ := json.Marshal(currentObject)
			base[key] = mergedObject
			continue
		}
		base[key] = value
	}
}

func buildPersonaToolSchema() json.RawMessage {
	user := personaPatchSchema(persona.UserSchemaDocument)
	soul := personaPatchSchema(persona.SoulSchemaDocument)
	document := map[string]any{"type": "object", "properties": map[string]any{
		"target": map[string]any{"type": "string", "enum": []string{"user", "soul"}},
		"patch":  map[string]any{"anyOf": []any{user, soul}},
	}, "required": []string{"target", "patch"}, "additionalProperties": false}
	encoded, _ := json.Marshal(document)
	return encoded
}

func buildPersonaUserUpdateSchema() json.RawMessage {
	patch := personaPatchSchema(persona.UserSchemaDocument)
	document := map[string]any{"type": "object", "properties": map[string]any{"target": map[string]any{"const": "user"}, "patch": patch}, "required": []string{"target", "patch"}, "additionalProperties": false}
	encoded, _ := json.Marshal(document)
	return encoded
}

func personaPatchSchema(document []byte) map[string]any {
	var schema map[string]any
	_ = json.Unmarshal(document, &schema)
	delete(schema, "$schema")
	delete(schema, "$id")
	delete(schema, "title")
	delete(schema, "description")
	delete(schema, "required")
	return schema
}

func buildPersonaToolOutputSchema() json.RawMessage {
	document := map[string]any{"type": "object", "properties": map[string]any{
		"target":   map[string]any{"type": "string", "enum": []string{"user", "soul"}},
		"document": map[string]any{"anyOf": []any{personaPatchSchema(persona.UserSchemaDocument), personaPatchSchema(persona.SoulSchemaDocument)}},
	}, "required": []string{"target", "document"}, "additionalProperties": false}
	encoded, _ := json.Marshal(document)
	return encoded
}

func buildPersonaToolIntentSchema() json.RawMessage {
	user := personaPatchSchema(persona.UserSchemaDocument)
	soul := personaPatchSchema(persona.SoulSchemaDocument)
	document := map[string]any{"type": "object", "properties": map[string]any{
		"target": map[string]any{"type": "string"},
		"patch":  map[string]any{"anyOf": []any{user, soul}},
	}, "additionalProperties": false}
	encoded, _ := json.Marshal(document)
	return encoded
}

func hasPolicyCircle(access policy.PersonAccess, circle string) bool {
	for _, candidate := range access.Circles {
		if candidate == circle {
			return true
		}
	}
	return false
}

func personaToolSuccess(target string, document []byte) toolcontract.ToolResult {
	var value json.RawMessage = document
	output := json.RawMessage(marshalToolResult(map[string]json.RawMessage{"target": json.RawMessage(`"` + target + `"`), "document": value}))
	return toolcontract.ToolSuccessData(string(output), output)
}
