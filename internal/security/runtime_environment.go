package security

const CanonicalRuntimePATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

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
}

func IsWorkspaceManagedEnvironmentName(name string) bool {
	return workspaceManagedEnvironmentNames[name]
}

func enforceCanonicalRuntimePATH(environmentVariables map[string]string) map[string]string {
	if environmentVariables == nil {
		environmentVariables = map[string]string{}
	}
	environmentVariables["PATH"] = CanonicalRuntimePATH
	return environmentVariables
}
