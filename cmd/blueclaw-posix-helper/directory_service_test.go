package main

import "testing"

func attributeValue(commands [][]string, key string) (string, bool) {
	for _, arguments := range commands {
		if len(arguments) == 5 && arguments[3] == key {
			return arguments[4], true
		}
	}
	return "", false
}

func requireAttribute(t *testing.T, commands [][]string, key string, expected string) {
	t.Helper()
	value, isPresent := attributeValue(commands, key)
	if !isPresent {
		t.Fatalf("expected %s to be set, got commands %v", key, commands)
	}
	if value != expected {
		t.Fatalf("expected %s to be %q, got %q", key, expected, value)
	}
}

func TestCreateUserCommandsCarryTheAllocatedIdentity(testInstance *testing.T) {
	commands := createUserCommands("bc_person_a1b2c3", "/workspace/private/people/a1b2c3", 100007, 100008)

	if commands[0][2] != "/Users/bc_person_a1b2c3" {
		testInstance.Fatalf("expected the record to be created first, got %v", commands[0])
	}
	requireAttribute(testInstance, commands, "UniqueID", "100007")
	requireAttribute(testInstance, commands, "PrimaryGroupID", "100008")
	requireAttribute(testInstance, commands, "NFSHomeDirectory", "/workspace/private/people/a1b2c3")
}

func TestCreateUserCommandsHideProjectedPeopleFromTheLoginWindow(testInstance *testing.T) {
	commands := createUserCommands("bc_person_a1b2c3", "/home/a1b2c3", 100007, 100008)

	requireAttribute(testInstance, commands, "IsHidden", "1")
}

func TestCreateUserCommandsUseAShellThatExistsOnMacOS(testInstance *testing.T) {
	commands := createUserCommands("bc_person_a1b2c3", "/home/a1b2c3", 100007, 100008)

	requireAttribute(testInstance, commands, "UserShell", "/usr/bin/false")
	if macOSServiceAccountShell == "/usr/sbin/nologin" {
		testInstance.Fatal("macOS ships no /usr/sbin/nologin; a user created with it cannot be used")
	}
}

func TestCreateGroupCommandsCarryTheAllocatedIdentity(testInstance *testing.T) {
	commands := createGroupCommands("bc_circle_member", 100004)

	if commands[0][2] != "/Groups/bc_circle_member" {
		testInstance.Fatalf("expected the record to be created first, got %v", commands[0])
	}
	requireAttribute(testInstance, commands, "PrimaryGroupID", "100004")
}

func TestParseDirectoryServiceListReadsNameAndIdentity(testInstance *testing.T) {
	identities := parseDirectoryServiceList("_amavisd                 83\nlee                      501\nbc_person_a1b2c3         100007\n")

	if len(identities) != 3 {
		testInstance.Fatalf("expected three identities, got %+v", identities)
	}
	if identities[1].name != "lee" || identities[1].identityID != 501 {
		testInstance.Fatalf("expected lee at 501, got %+v", identities[1])
	}
	if identities[2].name != "bc_person_a1b2c3" || identities[2].identityID != 100007 {
		testInstance.Fatalf("expected the projected person to be readable back, got %+v", identities[2])
	}
}

func TestParseDirectoryServiceListSkipsLinesWithoutAnIdentity(testInstance *testing.T) {
	identities := parseDirectoryServiceList("\nname only\n_ftp   98\ntrailing\n")

	if len(identities) != 1 || identities[0].identityID != 98 {
		testInstance.Fatalf("expected only the record carrying a numeric identity, got %+v", identities)
	}
}

func TestParseDirectoryServiceValueReadsTheThreeShapesDsclEmits(testInstance *testing.T) {
	for _, shape := range []struct {
		name   string
		output string
		value  string
	}{
		{"standard attribute", "UserShell: /bin/zsh\n", "/bin/zsh"},
		{"native attribute carries a type prefix", "dsAttrTypeNative:IsHidden: 1\n", "1"},
		{"a value containing a space moves to the next line", "RealName:\n dongha lee\n", "dongha lee"},
	} {
		if parsed := parseDirectoryServiceValue(shape.output); parsed != shape.value {
			testInstance.Fatalf("%s: expected %q, got %q", shape.name, shape.value, parsed)
		}
	}
}
