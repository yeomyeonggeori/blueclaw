package capability

import "encoding/json"

// The descriptor a product's catalog writes and this runtime reads. Every field
// the catalog carries is named here, because a field this struct omits is
// dropped silently on the way in and the runtime behaves as though the product
// never said it.
type ToolDescriptor struct {
	Name                     string              `json:"name"`
	CanonicalName            string              `json:"canonicalName"`
	Namespace                string              `json:"namespace"`
	AnsweredBy               string              `json:"answeredBy,omitempty"`
	ModelName                string              `json:"modelName"`
	ModelVisibility          string              `json:"modelVisibility"`
	ModelVisible             bool                `json:"modelVisible,omitempty"`
	Description              string              `json:"description,omitempty"`
	Version                  string              `json:"version,omitempty"`
	PrivacyClass             string              `json:"privacyClass,omitempty"`
	EstimatedLatency         string              `json:"estimatedLatency,omitempty"`
	RequiresUserPresence     bool                `json:"requiresUserPresence,omitempty"`
	RequiresRequesterDevice  bool                `json:"requiresRequesterDevice,omitempty"`
	RequiresCompanionBrowser bool                `json:"requiresCompanionBrowser,omitempty"`
	ApprovalScope            string              `json:"approvalScope,omitempty"`
	WorksOffline             bool                `json:"worksOffline,omitempty"`
	InputSchema              json.RawMessage     `json:"inputSchema,omitempty"`
	InputIntentSchema        json.RawMessage     `json:"inputIntentSchema,omitempty"`
	OutputSchema             json.RawMessage     `json:"outputSchema,omitempty"`
	InputSchemaStrict        bool                `json:"inputSchemaStrict,omitempty"`
	OutputSchemaStrict       bool                `json:"outputSchemaStrict,omitempty"`
	ResultContract           *ToolResultContract `json:"resultContract,omitempty"`
	PolicyResource           string              `json:"policyResource,omitempty"`
	SideEffectClass          string              `json:"sideEffectClass,omitempty"`
	SideEffect               string              `json:"sideEffect,omitempty"`
	RequiresApproval         bool                `json:"requiresApproval,omitempty"`
	CompletionEvidence       *CompletionEvidence `json:"completionEvidence,omitempty"`
	Availability             Availability        `json:"availability"`
	Idempotency              Idempotency         `json:"idempotency"`
}

type ToolResultContract struct {
	Schema            json.RawMessage          `json:"schema"`
	Effects           []ResourceEffectContract `json:"effects,omitempty"`
	EvidenceCondition *EvidenceCondition       `json:"evidenceCondition,omitempty"`
}

type EvidenceCondition struct {
	ResultField string          `json:"resultField"`
	Equals      json.RawMessage `json:"equals"`
}

type ResourceEffectContract struct {
	ObjectType     string             `json:"objectType"`
	Effect         string             `json:"effect"`
	ResultField    string             `json:"resultField"`
	EffectIdentity string             `json:"effectIdentity"`
	When           *EvidenceCondition `json:"when,omitempty"`
}

type CompletionEvidence struct {
	Mode       string `json:"mode,omitempty"`
	Action     string `json:"action,omitempty"`
	TargetKind string `json:"targetKind,omitempty"`
}

type Availability struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type Idempotency struct {
	Supported bool   `json:"supported"`
	Required  bool   `json:"required"`
	Scope     string `json:"scope,omitempty"`
}
