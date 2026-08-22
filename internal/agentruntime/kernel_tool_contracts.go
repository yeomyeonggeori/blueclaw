package agentruntime

import (
	"encoding/json"
	"errors"
	"strings"
)

const terminalRunModeCommand = "command"

var terminalRunInputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"approvalRequired":{"type":"boolean"},
		"approvalReason":{"type":"string","minLength":1},
		"command":{"type":"string","minLength":1},
		"workingDirectoryPath":{"type":"string"},
		"timeoutSecond":{"type":"integer","minimum":1}
	},
	"required":["command"],
	"additionalProperties":false
}`)

var terminalRunInputIntentSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

var terminalRunResultSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"mode":{"type":"string"},
		"completed":{"type":"boolean"},
		"exitCode":{"type":"integer"},
		"stdout":{"type":"string"},
		"stderr":{"type":"string"},
		"timedOut":{"type":"boolean"},
		"signal":{"type":"string"},
		"outputTrimmed":{"type":"boolean"}
	},
	"required":["mode","completed","exitCode","stdout","stderr","timedOut","outputTrimmed"],
	"additionalProperties":false,
	"oneOf":[
		{
			"properties":{"mode":{"const":"command"},"completed":{"const":true},"exitCode":{"const":0},"timedOut":{"const":false}},
			"required":["mode","completed","exitCode","timedOut"]
		},
		{
			"properties":{"mode":{"const":"command"},"completed":{"const":false}},
			"required":["mode","completed"],
			"not":{
				"properties":{"exitCode":{"const":0},"timedOut":{"const":false}},
				"required":["exitCode","timedOut"]
			}
		}
	]
}`)

type terminalRunToolInput struct {
	ApprovalRequired     *bool  `json:"approvalRequired"`
	ApprovalReason       string `json:"approvalReason"`
	Command              string `json:"command"`
	WorkingDirectoryPath string `json:"workingDirectoryPath"`
	TimeoutSecond        int    `json:"timeoutSecond"`
}

type terminalCommandResultDocument struct {
	Mode          string `json:"mode"`
	Completed     bool   `json:"completed"`
	ExitCode      int    `json:"exitCode"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	TimedOut      bool   `json:"timedOut"`
	Signal        string `json:"signal,omitempty"`
	OutputTrimmed bool   `json:"outputTrimmed"`
}

func validateTerminalRunInput(input terminalRunToolInput) error {
	isApprovalRequired := input.ApprovalRequired != nil && *input.ApprovalRequired
	if isApprovalRequired && strings.TrimSpace(input.ApprovalReason) == "" {
		return errors.New("approvalReason is required when approvalRequired is true")
	}
	if strings.TrimSpace(input.Command) == "" {
		return errors.New("command is required")
	}
	return nil
}
