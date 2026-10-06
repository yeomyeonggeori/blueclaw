package security

import "os"

// RuntimePATH is the PATH every command the agent runs is given: the one its
// supervisor started it with. The unit that starts the agent is what knows
// where this host keeps its programs, and a company host puts its own Python
// first there, so a requester's `python3` is the host's interpreter rather than
// whichever one the distribution ships.
func RuntimePATH() string {
	return os.Getenv("PATH")
}

var workspaceManagedEnvironmentNames = map[string]bool{
	"BLUECLAW_TASK_TMP":            true,
	"BLUECLAW_REQUESTER_ARTIFACTS": true,
	"HOME":                         true,
	"PATH":                         true,
	"TMPDIR":                       true,
	"TMP":                          true,
	"TEMP":                         true,
	"XDG_CACHE_HOME":               true,
	"XDG_CONFIG_HOME":              true,
	"XDG_RUNTIME_DIR":              true,
	"BUN_TMPDIR":                   true,
	"BUN_INSTALL":                  true,
	"BUN_INSTALL_CACHE_DIR":        true,
	"npm_config_cache":             true,
	"SKILL_TASK_CONTEXT":           true,
	"SKILL_HOST_URL":               true,
	"SKILL_HOST_TOKEN":             true,
}

func IsWorkspaceManagedEnvironmentName(name string) bool {
	return workspaceManagedEnvironmentNames[name]
}

func enforceRuntimePATH(environmentVariables map[string]string) map[string]string {
	if environmentVariables == nil {
		environmentVariables = map[string]string{}
	}
	environmentVariables["PATH"] = RuntimePATH()
	return environmentVariables
}
