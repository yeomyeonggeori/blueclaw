package adminapi

import "encoding/json"

var scheduleToolCreateInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"taskRunID": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"taskInstruction": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"description": {"type": "string"},
		"kind": {"type": "string", "enum": ["once", "interval", "cron"]},
		"runAt": {"type": "string", "format": "date-time"},
		"expiresAt": {"type": "string", "format": "date-time"},
		"intervalSecond": {"type": "integer", "minimum": 1},
		"cronExpression": {"type": "string"},
		"timeZone": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"maxRunCount": {"type": "integer", "minimum": 1},
		"repeatPolicy": {"type": "string", "enum": ["finite", "unbounded"]},
		"platform": {"type": "string"},
		"conversationID": {"type": "string"},
		"replyTargetID": {"type": "string"}
	},
	"required": ["taskInstruction", "kind"],
	"additionalProperties": false
}`)

var scheduleToolUpdateInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"scheduleHint": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"taskInstruction": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"description": {"type": "string"},
		"kind": {"type": "string", "enum": ["once", "interval", "cron"]},
		"runAt": {"type": "string"},
		"expiresAt": {"type": "string"},
		"intervalSecond": {"type": "integer", "minimum": 1},
		"cronExpression": {"type": "string"},
		"timeZone": {"type": "string", "minLength": 1, "pattern": "\\S"},
		"maxRunCount": {"type": "integer", "minimum": 1},
		"repeatPolicy": {"type": "string", "enum": ["finite", "unbounded"]}
	},
	"required": ["scheduleHint"],
	"additionalProperties": false
}`)

var scheduleToolCancelInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"scheduleHints": {
			"type": "array",
			"items": {"type": "string", "minLength": 1, "pattern": "\\S"},
			"minItems": 1,
			"uniqueItems": true
		}
	},
	"required": ["scheduleHints"],
	"additionalProperties": false
}`)

var scheduleToolMutationOutputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"scheduleID": {"type": "string", "minLength": 1},
		"description": {"type": "string"},
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
		"description",
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

var scheduleToolCancelOutputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"cancelled": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"scheduleID": {"type": "string", "minLength": 1},
					"description": {"type": "string"}
				},
				"required": ["scheduleID", "description"],
				"additionalProperties": false
			}
		}
	},
	"required": ["cancelled"],
	"additionalProperties": false
}`)
