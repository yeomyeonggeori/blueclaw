package security

type CommandRequest struct {
	Command              string            `json:"command"`
	ExecutableName       string            `json:"executableName"`
	Arguments            []string          `json:"arguments"`
	Stdin                string            `json:"stdin"`
	WorkingDirectoryPath string            `json:"workingDirectoryPath"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
	TimeoutSecond        int               `json:"timeoutSecond"`
	OutputMaximumBytes   int               `json:"outputMaximumBytes"`
	IsInteractive        bool              `json:"isInteractive"`
	IsPTY                bool              `json:"isPTY"`
	IsHarnessAgent       bool              `json:"isHarnessAgent"`
	ExecutionIdentity    ExecutionIdentity `json:"-"`
}
