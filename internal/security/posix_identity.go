package security

import (
	"crypto/sha1"
	"encoding/hex"
	"os/user"
	"strconv"
	"strings"
	"unicode"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

const (
	blueclawServiceUserName = "blueclaw"
	posixSharedGroupName    = "bc_shared"
	posixNameMaximumLength  = 31
)

type ExecutionIdentity struct {
	UserName                string   `json:"-"`
	GroupName               string   `json:"-"`
	SupplementaryGroupNames []string `json:"-"`
	HomeDirectoryPath       string   `json:"-"`
	UserID                  uint32   `json:"-"`
	GroupID                 uint32   `json:"-"`
	SupplementaryGroupIDs   []uint32 `json:"-"`
}

type POSIXState struct {
	Users       []POSIXUser      `json:"users"`
	Groups      []POSIXGroup     `json:"groups"`
	Directories []POSIXDirectory `json:"directories"`
}

type POSIXUser struct {
	Name      string   `json:"name"`
	HomePath  string   `json:"homePath"`
	GroupName string   `json:"groupName"`
	Groups    []string `json:"groups"`
}

type POSIXGroup struct {
	Name string `json:"name"`
}

type POSIXDirectory struct {
	Path     string `json:"path"`
	Owner    string `json:"owner"`
	Group    string `json:"group"`
	ModeText string `json:"modeText"`
}

func ExecutionIdentityForPersonAccess(personAccess policy.PersonAccess, workspaceRootPath string) ExecutionIdentity {
	personID := strings.TrimSpace(personAccess.PersonID)
	if personID == "" {
		return ExecutionIdentity{}
	}
	personAccess = policy.EnsureRequesterDefaults(personAccess)

	groupNames := []string{posixSharedGroupName}
	for _, circleID := range personAccess.Circles {
		normalizedCircleID := strings.ToLower(strings.TrimSpace(circleID))
		if normalizedCircleID == "" || normalizedCircleID == "admin" {
			continue
		}
		groupNames = append(groupNames, LinuxCircleGroupName(normalizedCircleID))
	}

	userName := LinuxPersonUserName(personID)
	return ExecutionIdentity{
		UserName:                userName,
		GroupName:               userName,
		SupplementaryGroupNames: uniqueStrings(groupNames),
		HomeDirectoryPath:       PersonHomeDirectoryPath(workspaceRootPath, personID),
	}
}

func ResolveExecutionIdentity(identity ExecutionIdentity) (ExecutionIdentity, error) {
	if strings.TrimSpace(identity.UserName) == "" {
		return ExecutionIdentity{}, nil
	}

	resolvedUser, errorValue := user.Lookup(identity.UserName)
	if errorValue != nil {
		return ExecutionIdentity{}, errorValue
	}
	userID, errorValue := parseUnsignedID(resolvedUser.Uid)
	if errorValue != nil {
		return ExecutionIdentity{}, errorValue
	}

	groupName := firstNonEmptyString(identity.GroupName, identity.UserName)
	resolvedGroup, errorValue := user.LookupGroup(groupName)
	if errorValue != nil {
		return ExecutionIdentity{}, errorValue
	}
	groupID, errorValue := parseUnsignedID(resolvedGroup.Gid)
	if errorValue != nil {
		return ExecutionIdentity{}, errorValue
	}

	groupIDs := []uint32{}
	for _, groupName := range identity.SupplementaryGroupNames {
		resolvedGroup, errorValue := user.LookupGroup(groupName)
		if errorValue != nil {
			return ExecutionIdentity{}, errorValue
		}
		groupID, errorValue := parseUnsignedID(resolvedGroup.Gid)
		if errorValue != nil {
			return ExecutionIdentity{}, errorValue
		}
		groupIDs = append(groupIDs, groupID)
	}

	identity.UserID = userID
	identity.GroupID = groupID
	identity.SupplementaryGroupIDs = uniqueUnsignedIntegers(groupIDs)
	return identity, nil
}

func applyPOSIXEnvironment(environmentVariables map[string]string, identity ExecutionIdentity) map[string]string {
	result := map[string]string{}
	for name, value := range environmentVariables {
		result[name] = value
	}
	if strings.TrimSpace(identity.HomeDirectoryPath) != "" {
		result["HOME"] = identity.HomeDirectoryPath
		setDefaultEnvironmentValue(result, "BLUECLAW_REQUESTER_TMP", RequesterTemporaryDirectoryPath(identity.HomeDirectoryPath))
		setDefaultEnvironmentValue(result, "BLUECLAW_REQUESTER_ARTIFACTS", identity.HomeDirectoryPath+"/artifacts")
	}
	requesterTmpPath := firstNonEmptyString(result["BLUECLAW_REQUESTER_TMP"], identity.HomeDirectoryPath+"/tmp")
	runtimeRootPath := requesterTmpPath + "/.runtime"
	scratchRootPath := firstNonEmptyString(result["BLUECLAW_TASK_TMP"], runtimeRootPath)
	dependencyCachePath := "/workspace/shared/cache/dependencies"
	setDefaultEnvironmentValue(result, "BLUECLAW_DEPENDENCY_CACHE", dependencyCachePath)
	setDefaultEnvironmentValue(result, "TMPDIR", scratchRootPath+"/tmp")
	setDefaultEnvironmentValue(result, "TMP", scratchRootPath+"/tmp")
	setDefaultEnvironmentValue(result, "TEMP", scratchRootPath+"/tmp")
	setDefaultEnvironmentValue(result, "XDG_CACHE_HOME", runtimeRootPath+"/cache")
	setDefaultEnvironmentValue(result, "XDG_CONFIG_HOME", runtimeRootPath+"/config")
	setDefaultEnvironmentValue(result, "XDG_RUNTIME_DIR", runtimeRootPath+"/runtime")
	setDefaultEnvironmentValue(result, "BUN_TMPDIR", runtimeRootPath+"/bun/tmp")
	setDefaultEnvironmentValue(result, "BUN_INSTALL", runtimeRootPath+"/bun/install")
	setDefaultEnvironmentValue(result, "BUN_INSTALL_CACHE_DIR", dependencyCachePath+"/bun")
	setDefaultEnvironmentValue(result, "npm_config_cache", runtimeRootPath+"/npm")
	setDefaultEnvironmentValue(result, "GOMODCACHE", dependencyCachePath+"/go/pkg/mod")
	setDefaultEnvironmentValue(result, "GOCACHE", dependencyCachePath+"/go/build")
	setDefaultEnvironmentValue(result, "PIP_CACHE_DIR", dependencyCachePath+"/pip")
	setDefaultEnvironmentValue(result, "UV_CACHE_DIR", dependencyCachePath+"/uv")
	setDefaultEnvironmentValue(result, "CARGO_HOME", dependencyCachePath+"/cargo")
	return enforceCanonicalRuntimePATH(result)
}

func setDefaultEnvironmentValue(environmentVariables map[string]string, name string, value string) {
	if strings.TrimSpace(environmentVariables[name]) == "" {
		environmentVariables[name] = value
	}
}

func POSIXStateForPolicy(policyDocument policy.PolicyDocument, workspaceRootPath string) POSIXState {
	state := POSIXState{
		Groups: []POSIXGroup{{Name: posixSharedGroupName}},
		Directories: []POSIXDirectory{
			{Path: workspaceRootPath + "/circles", Owner: blueclawServiceUserName, Group: blueclawServiceUserName, ModeText: "0711"},
			{Path: workspaceRootPath + "/private", Owner: blueclawServiceUserName, Group: blueclawServiceUserName, ModeText: "0711"},
			{Path: workspaceRootPath + "/private/people", Owner: blueclawServiceUserName, Group: blueclawServiceUserName, ModeText: "0711"},
			{Path: workspaceRootPath + "/shared", Owner: blueclawServiceUserName, Group: posixSharedGroupName, ModeText: "2755"},
			{Path: workspaceRootPath + "/shared/public", Owner: blueclawServiceUserName, Group: posixSharedGroupName, ModeText: "2775"},
			{Path: workspaceRootPath + "/shared/cache", Owner: blueclawServiceUserName, Group: posixSharedGroupName, ModeText: "2775"},
			{Path: workspaceRootPath + "/shared/cache/dependencies", Owner: blueclawServiceUserName, Group: posixSharedGroupName, ModeText: "2775"},
			{Path: workspaceRootPath + "/shared/cache/dependencies/bun", Owner: blueclawServiceUserName, Group: posixSharedGroupName, ModeText: "2775"},
			{Path: workspaceRootPath + "/shared/cache/dependencies/uv", Owner: blueclawServiceUserName, Group: posixSharedGroupName, ModeText: "2775"},
			{Path: workspaceRootPath + "/shared/cache/weather", Owner: blueclawServiceUserName, Group: posixSharedGroupName, ModeText: "2775"},
			{Path: workspaceRootPath + "/shared/cache/weather/open-meteo-v1", Owner: blueclawServiceUserName, Group: posixSharedGroupName, ModeText: "2775"},
		},
	}

	for _, circlePolicy := range circlePoliciesWithStaffDefault(policyDocument.Circles, workspaceRootPath) {
		circleID := strings.ToLower(strings.TrimSpace(circlePolicy.CircleID))
		if circleID == "" {
			continue
		}
		groupName := LinuxCircleGroupName(circleID)
		circleWorkspacePath := firstNonEmptyString(circlePolicy.WorkspaceDirectoryPath, workspaceRootPath+"/circles/"+circleID)
		state.Groups = append(state.Groups, POSIXGroup{Name: groupName})
		state.Directories = append(state.Directories, POSIXDirectory{
			Path:     circleWorkspacePath,
			Owner:    blueclawServiceUserName,
			Group:    groupName,
			ModeText: "2770",
		})
		if circleID == policy.MemberCircleID {
			state.Directories = append(state.Directories, POSIXDirectory{
				Path:     strings.TrimRight(circleWorkspacePath, "/") + "/sites",
				Owner:    blueclawServiceUserName,
				Group:    groupName,
				ModeText: "2770",
			})
		}
	}

	for _, personPolicy := range policyDocument.People {
		personID := strings.TrimSpace(personPolicy.PersonID)
		if personID == "" {
			continue
		}
		userName := LinuxPersonUserName(personID)
		groupNames := []string{userName, posixSharedGroupName}
		for _, circleID := range effectivePersonCirclesForPOSIX(personPolicy) {
			groupNames = append(groupNames, LinuxCircleGroupName(circleID))
		}
		state.Groups = append(state.Groups, POSIXGroup{Name: userName})
		state.Users = append(state.Users, POSIXUser{
			Name:      userName,
			HomePath:  PersonHomeDirectoryPath(workspaceRootPath, personID),
			GroupName: userName,
			Groups:    uniqueStrings(groupNames),
		})
		state.Directories = append(state.Directories, POSIXDirectory{
			Path:     PersonHomeDirectoryPath(workspaceRootPath, personID),
			Owner:    userName,
			Group:    userName,
			ModeText: "0700",
		})
		state.Directories = append(state.Directories, POSIXDirectory{
			Path:     RequesterTemporaryDirectoryPath(PersonHomeDirectoryPath(workspaceRootPath, personID)),
			Owner:    userName,
			Group:    userName,
			ModeText: "0700",
		})
		state.Directories = append(state.Directories, POSIXDirectory{
			Path:     PersonHomeDirectoryPath(workspaceRootPath, personID) + "/artifacts",
			Owner:    userName,
			Group:    userName,
			ModeText: "0700",
		})
	}

	return normalizePOSIXState(state)
}

func LinuxPersonUserName(personID string) string {
	return shortenedLinuxName("bc_person_", personID)
}

func LinuxCircleGroupName(circleID string) string {
	return shortenedLinuxName("bc_circle_", circleID)
}

func shortenedLinuxName(prefix string, value string) string {
	normalizedValue := normalizeLinuxNameValue(value)
	if hasLossyLinuxNameNormalization(value, normalizedValue) {
		normalizedValue = linuxNameValueWithSuffix(normalizedValue, value, posixNameMaximumLength-len(prefix))
	}
	candidate := prefix + normalizedValue
	if len(candidate) <= posixNameMaximumLength {
		return candidate
	}
	hashValue := shortHash(value)
	valueLength := posixNameMaximumLength - len(prefix) - len(hashValue) - 1
	if valueLength < 1 {
		valueLength = 1
	}
	return prefix + strings.Trim(normalizedValue[:valueLength], "_-") + "_" + hashValue
}

func normalizeLinuxNameValue(value string) string {
	builder := strings.Builder{}
	for _, runeValue := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case runeValue >= 'a' && runeValue <= 'z':
			builder.WriteRune(runeValue)
		case runeValue >= '0' && runeValue <= '9':
			builder.WriteRune(runeValue)
		case runeValue == '-' || runeValue == '_':
			builder.WriteRune(runeValue)
		case unicode.IsSpace(runeValue):
			builder.WriteRune('_')
		default:
			builder.WriteRune('_')
		}
	}
	normalizedValue := strings.Trim(builder.String(), "_-")
	if normalizedValue == "" {
		return "id_" + shortHash(value)
	}
	if normalizedValue[0] >= '0' && normalizedValue[0] <= '9' {
		return "id_" + normalizedValue
	}
	return normalizedValue
}

func hasLossyLinuxNameNormalization(value string, normalizedValue string) bool {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return true
	}
	if trimmedValue[0] >= '0' && trimmedValue[0] <= '9' {
		return true
	}
	return strings.ToLower(trimmedValue) != normalizedValue
}

func linuxNameValueWithSuffix(normalizedValue string, value string, maximumLength int) string {
	hashValue := shortHash(value)
	valueLength := maximumLength - len(hashValue) - 1
	if valueLength < 1 {
		return hashValue[:maximumLength]
	}
	shortenedValue := strings.Trim(normalizedValue, "_-")
	if len(shortenedValue) > valueLength {
		shortenedValue = strings.Trim(shortenedValue[:valueLength], "_-")
	}
	if shortenedValue == "" {
		shortenedValue = "id"
	}
	return shortenedValue + "_" + hashValue
}

func effectivePersonCirclesForPOSIX(personPolicy policy.PersonPolicy) []string {
	circles := append([]string{policy.MemberCircleID}, personPolicy.Circles...)
	if personPolicy.IsAdmin {
		circles = append(circles, policy.AdminCircleID)
	}
	return uniqueStrings(normalizeStringList(circles))
}

func circlePoliciesWithStaffDefault(circlePolicies []policy.CirclePolicy, workspaceRootPath string) []policy.CirclePolicy {
	hasStaffCircle := false
	result := append([]policy.CirclePolicy{}, circlePolicies...)
	for _, circlePolicy := range circlePolicies {
		if strings.ToLower(strings.TrimSpace(circlePolicy.CircleID)) == policy.MemberCircleID {
			hasStaffCircle = true
			break
		}
	}
	if !hasStaffCircle {
		result = append(result, policy.CirclePolicy{
			CircleID:               policy.MemberCircleID,
			WorkspaceDirectoryPath: workspaceRootPath + "/circles/" + policy.MemberCircleID,
		})
	}
	return result
}

func normalizePOSIXState(state POSIXState) POSIXState {
	groupByName := map[string]POSIXGroup{}
	for _, group := range state.Groups {
		if strings.TrimSpace(group.Name) != "" {
			groupByName[group.Name] = group
		}
	}
	for _, user := range state.Users {
		groupByName[user.GroupName] = POSIXGroup{Name: user.GroupName}
		for _, groupName := range user.Groups {
			if strings.TrimSpace(groupName) != "" {
				groupByName[groupName] = POSIXGroup{Name: groupName}
			}
		}
	}

	groups := []POSIXGroup{}
	for _, group := range groupByName {
		groups = append(groups, group)
	}
	state.Groups = groups
	return state
}

func parseUnsignedID(value string) (uint32, error) {
	parsedValue, errorValue := strconv.ParseUint(value, 10, 32)
	return uint32(parsedValue), errorValue
}

func formatUnsignedID(value uint32) string {
	return strconv.FormatUint(uint64(value), 10)
}

func joinUnsignedIDs(values []uint32) string {
	parts := []string{}
	for _, value := range values {
		parts = append(parts, formatUnsignedID(value))
	}
	return strings.Join(parts, ",")
}

func shortHash(value string) string {
	hashValue := sha1.Sum([]byte(value))
	return hex.EncodeToString(hashValue[:])[:8]
}

func uniqueStrings(values []string) []string {
	seenValue := map[string]bool{}
	result := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || seenValue[trimmedValue] {
			continue
		}
		seenValue[trimmedValue] = true
		result = append(result, trimmedValue)
	}
	return result
}

func normalizeStringList(values []string) []string {
	result := []string{}
	for _, value := range values {
		normalizedValue := strings.ToLower(strings.TrimSpace(value))
		if normalizedValue != "" {
			result = append(result, normalizedValue)
		}
	}
	return result
}

func uniqueUnsignedIntegers(values []uint32) []uint32 {
	seenValue := map[uint32]bool{}
	result := []uint32{}
	for _, value := range values {
		if seenValue[value] {
			continue
		}
		seenValue[value] = true
		result = append(result, value)
	}
	return result
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
