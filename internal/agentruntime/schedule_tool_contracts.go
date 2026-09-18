package agentruntime

import (
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

var scheduleListInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"status": {"type": "string", "enum": ["active", "failed", "expired"]},
		"limit": {"type": "integer", "minimum": 1, "maximum": 9007199254740991}
	},
	"additionalProperties": false
}`)

var scheduleCreateInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string"},
		"taskInstruction": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"agentProfileName": {"type": "string"},
		"kind": {"type": "string", "enum": ["once", "interval", "cron"]},
		"runAt": {"type": "string", "format": "date-time"},
		"expiresAt": {"type": "string", "format": "date-time"},
		"intervalSecond": {"type": "integer", "minimum": 1},
		"cronExpression": {"type": "string"},
		"timeZone": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"maxRunCount": {"type": "integer", "minimum": 1},
		"repeatPolicy": {"type": "string", "enum": ["finite", "unbounded"]}
	},
	"required": ["taskInstruction", "kind"],
	"additionalProperties": false
}`)

var scheduleCreateInputIntentSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string"},
		"taskInstruction": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"agentProfileName": {"type": "string"},
		"kind": {"type": "string", "enum": ["once", "interval", "cron"]},
		"runAt": {"type": "string", "format": "date-time"},
		"expiresAt": {"type": "string", "format": "date-time"},
		"intervalSecond": {"type": "integer", "minimum": 1},
		"cronExpression": {"type": "string"},
		"timeZone": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"maxRunCount": {"type": "integer", "minimum": 1},
		"repeatPolicy": {"type": "string", "enum": ["finite", "unbounded"]}
	},
	"additionalProperties": false
}`)

var scheduleUpdateInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"scheduleID": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
		"name": {"type": "string"},
		"taskInstruction": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"agentProfileName": {"type": "string"},
		"kind": {"type": "string", "enum": ["once", "interval", "cron"]},
		"runAt": {"type": "string", "format": "date-time"},
		"expiresAt": {"type": "string", "format": "date-time"},
		"intervalSecond": {"type": "integer", "minimum": 1},
		"cronExpression": {"type": "string"},
		"timeZone": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"maxRunCount": {"type": "integer", "minimum": 1},
		"repeatPolicy": {"type": "string", "enum": ["finite", "unbounded"]}
	},
	"required": ["scheduleID"],
	"additionalProperties": false
}`)

var scheduleUpdateInputIntentSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"scheduleID": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
		"name": {"type": "string"},
		"taskInstruction": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"agentProfileName": {"type": "string"},
		"kind": {"type": "string", "enum": ["once", "interval", "cron"]},
		"runAt": {"type": "string", "format": "date-time"},
		"expiresAt": {"type": "string", "format": "date-time"},
		"intervalSecond": {"type": "integer", "minimum": 1},
		"cronExpression": {"type": "string"},
		"timeZone": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"maxRunCount": {"type": "integer", "minimum": 1},
		"repeatPolicy": {"type": "string", "enum": ["finite", "unbounded"]}
	},
	"additionalProperties": false
}`)

var scheduleCancelInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"scope": {"type": "string", "enum": ["currentConversation", "mine", "scheduleIDs"]},
		"scheduleIDs": {
			"type": "array",
			"items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
			"minItems": 1,
			"uniqueItems": true
		}
	},
	"required": ["scope"],
	"additionalProperties": false
}`)

var scheduleCancelInputIntentSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"scope": {"type": "string", "enum": ["currentConversation", "mine", "scheduleIDs"]},
		"scheduleIDs": {
			"type": "array",
			"items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
			"minItems": 1,
			"uniqueItems": true
		}
	},
	"additionalProperties": false
}`)

var scheduleListOutputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"schedules": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"scheduleID": {"type": "string", "minLength": 1},
					"taskInstruction": {"type": "string", "minLength": 1, "pattern": "\\S"},
					"description": {"type": "string"},
					"cadence": {"type": "string"},
					"cronExpression": {"type": "string"},
					"runAt": {"type": "string", "format": "date-time"},
					"status": {"type": "string", "enum": ["active", "failed", "expired"]},
					"nextRunAt": {"type": "string", "format": "date-time"},
					"lastRunAt": {"type": "string", "format": "date-time"}
				},
				"required": ["scheduleID", "taskInstruction", "cadence", "status"],
				"additionalProperties": false
			}
		}
	},
	"required": ["schedules"],
	"additionalProperties": false
}`)

var scheduleMutationResultSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"scheduleID": {"type": "string", "minLength": 1},
		"name": {"type": "string"},
		"taskInstruction": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"timeZone": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"kind": {"type": "string", "enum": ["once", "interval", "cron"]},
		"runAt": {"type": "string", "format": "date-time"},
		"intervalSecond": {"type": "integer", "minimum": 1},
		"cronExpression": {"type": "string"},
		"maxRunCount": {"type": "integer", "minimum": 1},
		"expiresAt": {"type": "string", "format": "date-time"},
		"nextRunAt": {"type": "string", "format": "date-time"},
		"conversationID": {"type": "string"},
		"replyTargetID": {"type": "string"},
		"agentProfileName": {"type": "string"}
	},
	"required": [
		"scheduleID",
		"name",
		"taskInstruction",
		"timeZone",
		"kind",
		"nextRunAt",
		"conversationID",
		"replyTargetID",
		"agentProfileName"
	],
	"additionalProperties": false
}`)

var scheduleCancelResultSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"cancelledScheduleIDs": {
			"type": "array",
			"items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
			"uniqueItems": true
		},
		"cancelledScheduleCount": {"type": "integer", "minimum": 0},
		"cancelledTaskRunCount": {"type": "integer", "minimum": 0},
		"cancelledWaitCount": {"type": "integer", "minimum": 0},
		"effectiveCancellationCount": {"type": "integer", "minimum": 0},
		"cancelled": {"type": "boolean"}
	},
	"required": [
		"cancelledScheduleIDs",
		"cancelledScheduleCount",
		"cancelledTaskRunCount",
		"cancelledWaitCount",
		"effectiveCancellationCount",
		"cancelled"
	],
	"allOf": [
		{
			"if": {"properties": {"cancelled": {"const": true}}, "required": ["cancelled"]},
			"then": {"properties": {"effectiveCancellationCount": {"minimum": 1}}}
		},
		{
			"if": {"properties": {"cancelled": {"const": false}}, "required": ["cancelled"]},
			"then": {"properties": {"effectiveCancellationCount": {"const": 0}}}
		}
	],
	"additionalProperties": false
}`)

func scheduleListResultContract() *toolcontract.ToolResultContract {
	return &toolcontract.ToolResultContract{Schema: scheduleListOutputSchema}
}

func scheduleMutationResultContract(effect string) *toolcontract.ToolResultContract {
	return &toolcontract.ToolResultContract{
		Schema: scheduleMutationResultSchema,
		Effects: []toolcontract.ResourceEffectContract{{
			ObjectType:     "schedule",
			Effect:         effect,
			ResultField:    "scheduleID",
			EffectIdentity: "id",
		}},
	}
}

func scheduleCancelResultContract() *toolcontract.ToolResultContract {
	return &toolcontract.ToolResultContract{
		Schema: scheduleCancelResultSchema,
		EvidenceCondition: &toolcontract.EvidenceCondition{
			ResultField: "cancelled",
			Equals:      json.RawMessage(`true`),
		},
	}
}
