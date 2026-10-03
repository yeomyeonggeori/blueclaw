//go:build darwin

package main

import (
	"fmt"
	"os/exec"
)

func createGroup(name string, groupID uint32) error {
	return runDirectoryServiceCommands(createGroupCommands(name, groupID))
}

func deleteGroup(name string) error {
	return runCommand("dscl", ".", "-delete", groupRecordPath(name))
}

func setGroupID(name string, groupID uint32) error {
	return runCommand("dscl", ".", "-create", groupRecordPath(name), "PrimaryGroupID", formatID(groupID))
}

func createUser(name string, homePath string, userID uint32, groupID uint32) error {
	return runDirectoryServiceCommands(createUserCommands(name, homePath, userID, groupID))
}

func setUserID(name string, userID uint32) error {
	return runCommand("dscl", ".", "-create", userRecordPath(name), "UniqueID", formatID(userID))
}

func setUserPrimaryGroupID(name string, groupID uint32) error {
	return runCommand("dscl", ".", "-create", userRecordPath(name), "PrimaryGroupID", formatID(groupID))
}

func addUserToGroup(userName string, groupName string) error {
	return runCommand("dseditgroup", "-o", "edit", "-a", userName, "-t", "user", groupName)
}

func runDirectoryServiceCommands(commands [][]string) error {
	for _, arguments := range commands {
		if errorValue := runCommand("dscl", arguments...); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func readSystemUsers() ([]systemIdentity, error) {
	return readDirectoryServiceIdentities("/Users", "UniqueID")
}

func readSystemGroups() ([]systemIdentity, error) {
	return readDirectoryServiceIdentities("/Groups", "PrimaryGroupID")
}

func readDirectoryServiceIdentities(recordType string, identityKey string) ([]systemIdentity, error) {
	output, errorValue := exec.Command("dscl", ".", "-list", recordType, identityKey).Output()
	if errorValue != nil {
		return nil, fmt.Errorf("dscl . -list %s %s: %w", recordType, identityKey, errorValue)
	}
	return parseDirectoryServiceList(string(output)), nil
}
