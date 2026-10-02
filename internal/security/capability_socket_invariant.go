package security

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

const capabilitySocketOtherAccessMask = 0o007

type CapabilitySocketInvariantResult struct {
	Skipped    bool
	SkipReason string
	SocketPath string
	GroupName  string
	Mode       os.FileMode
}

type CapabilitySocketGroupResolver func(socketPath string) (groupName string, mode os.FileMode, resolveError error)

type RequesterGroupNamesResolver func() ([]string, error)

func VerifyCapabilitySocketInvariant(socketPath string, resolveSocketGroup CapabilitySocketGroupResolver, resolveRequesterGroupNames RequesterGroupNamesResolver) (CapabilitySocketInvariantResult, error) {
	trimmedSocketPath := strings.TrimSpace(socketPath)
	if trimmedSocketPath == "" {
		return CapabilitySocketInvariantResult{
			Skipped:    true,
			SkipReason: "capabilities.unixSocketPath is not configured (HTTP transport in use)",
		}, nil
	}

	groupName, mode, resolveError := resolveSocketGroup(trimmedSocketPath)
	if errors.Is(resolveError, os.ErrNotExist) {
		return CapabilitySocketInvariantResult{
			Skipped:    true,
			SkipReason: "capability socket does not exist yet at " + trimmedSocketPath,
			SocketPath: trimmedSocketPath,
		}, nil
	}
	if resolveError != nil {
		return CapabilitySocketInvariantResult{}, fmt.Errorf("capability socket invariant: stat %s: %w", trimmedSocketPath, resolveError)
	}

	result := CapabilitySocketInvariantResult{
		SocketPath: trimmedSocketPath,
		GroupName:  groupName,
		Mode:       mode,
	}

	if mode&capabilitySocketOtherAccessMask != 0 {
		return result, fmt.Errorf(
			"capability socket invariant violated: %s has mode %04o which grants access to other users; capabilityd must create the socket with a mode that excludes other-access (see internal/capabilityd/TRUST_BOUNDARY.md)",
			trimmedSocketPath, mode,
		)
	}

	requesterGroupNames, requesterGroupsError := resolveRequesterGroupNames()
	if requesterGroupsError != nil {
		return result, fmt.Errorf("capability socket invariant: resolve requester group names: %w", requesterGroupsError)
	}

	for _, requesterGroupName := range requesterGroupNames {
		if requesterGroupName == groupName {
			return result, fmt.Errorf(
				"capability socket invariant violated: %s is owned by group %q (mode %04o), and projected requester identities are members of that group via %q; capabilityd must own the socket with a group that projected bc_person_* identities do not belong to (see internal/capabilityd/TRUST_BOUNDARY.md)",
				trimmedSocketPath, groupName, mode, requesterGroupName,
			)
		}
	}

	return result, nil
}

func EnsureCapabilitySocketInvariant(socketPath string, policyDocument policy.PolicyDocument) (CapabilitySocketInvariantResult, error) {
	if runtime.GOOS != "linux" {
		return CapabilitySocketInvariantResult{
			Skipped:    true,
			SkipReason: "capability socket invariant only applies on linux, running on " + runtime.GOOS,
		}, nil
	}
	return VerifyCapabilitySocketInvariant(socketPath, resolveCapabilitySocketGroupFromFilesystem, func() ([]string, error) {
		return requesterGroupNamesForPolicy(policyDocument), nil
	})
}

func resolveCapabilitySocketGroupFromFilesystem(socketPath string) (string, os.FileMode, error) {
	fileInfo, statError := os.Stat(socketPath)
	if statError != nil {
		return "", 0, statError
	}
	statInformation, isUnixStat := fileInfo.Sys().(*syscall.Stat_t)
	if !isUnixStat {
		return "", 0, fmt.Errorf("capability socket invariant: unable to read owning group for %s", socketPath)
	}
	groupIdentifier, groupLookupError := user.LookupGroupId(strconv.FormatUint(uint64(statInformation.Gid), 10))
	if groupLookupError != nil {
		return "", 0, fmt.Errorf("capability socket invariant: lookup group for gid %d: %w", statInformation.Gid, groupLookupError)
	}
	return groupIdentifier.Name, fileInfo.Mode().Perm(), nil
}

func requesterGroupNamesForPolicy(policyDocument policy.PolicyDocument) []string {
	posixState := POSIXStateForPolicy(policyDocument, "")
	groupNames := make([]string, 0, len(posixState.Groups))
	for _, group := range posixState.Groups {
		groupNames = append(groupNames, group.Name)
	}
	return groupNames
}
