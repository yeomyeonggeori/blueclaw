//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
)

const linuxServiceAccountShell = "/usr/sbin/nologin"

func createGroup(name string, groupID uint32) error {
	return runCommand("groupadd", "--system", "--gid", formatID(groupID), name)
}

func deleteGroup(name string) error {
	return runCommand("groupdel", name)
}

func setGroupID(name string, groupID uint32) error {
	return runCommand("groupmod", "--gid", formatID(groupID), name)
}

func createUser(name string, homePath string, userID uint32, groupID uint32) error {
	return runCommand(
		"useradd",
		"--system",
		"--uid", formatID(userID),
		"--gid", formatID(groupID),
		"--no-create-home",
		"--home-dir", homePath,
		"--shell", linuxServiceAccountShell,
		name,
	)
}

func setUserID(name string, userID uint32) error {
	return runCommand("usermod", "--uid", formatID(userID), name)
}

func setUserPrimaryGroupID(name string, groupID uint32) error {
	return runCommand("usermod", "--gid", formatID(groupID), name)
}

func addUserToGroup(userName string, groupName string) error {
	return runCommand("usermod", "-a", "-G", groupName, userName)
}

func readSystemUsers() ([]systemIdentity, error) {
	return readColonIdentities("/etc/passwd")
}

func readSystemGroups() ([]systemIdentity, error) {
	return readColonIdentities("/etc/group")
}

func readColonIdentities(path string) ([]systemIdentity, error) {
	records, errorValue := readColonRecords(path)
	if errorValue != nil {
		return nil, errorValue
	}
	identities := []systemIdentity{}
	for _, fields := range records {
		identityID, isValid := parseColonIdentityID(fields, 2)
		if !isValid {
			continue
		}
		identities = append(identities, systemIdentity{name: fields[0], identityID: identityID})
	}
	return identities, nil
}

func readColonRecords(path string) ([][]string, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return nil, errorValue
	}
	records := [][]string{}
	for _, line := range strings.Split(string(document), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		records = append(records, strings.Split(line, ":"))
	}
	return records, nil
}

func parseColonIdentityID(fields []string, index int) (uint32, bool) {
	if len(fields) <= index {
		return 0, false
	}
	identityID, errorValue := strconv.ParseUint(fields[index], 10, 32)
	if errorValue != nil {
		return 0, false
	}
	return uint32(identityID), true
}
