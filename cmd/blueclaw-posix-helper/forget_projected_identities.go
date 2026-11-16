package main

import (
	"errors"
	"fmt"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

var errForgettingIdentitiesTakesRoot = errors.New("only root forgets the projected identities: it removes every person's account at once, which the agent's own user never needs")

func runForgetProjectedIdentities(realUserID int, arguments []string) error {
	if realUserID != 0 {
		return errForgettingIdentitiesTakesRoot
	}
	if len(arguments) != 0 {
		return fmt.Errorf("forget-projected-identities takes no arguments, got %q", arguments)
	}
	if errorValue := forgetProjected(readSystemUsers, deleteUser); errorValue != nil {
		return fmt.Errorf("removing the projected users: %w", errorValue)
	}
	if errorValue := forgetProjected(readSystemGroups, deleteGroup); errorValue != nil {
		return fmt.Errorf("removing the projected groups: %w", errorValue)
	}
	return nil
}

func forgetProjected(read func() ([]systemIdentity, error), remove func(name string) error) error {
	identities, errorValue := read()
	if errorValue != nil {
		return errorValue
	}
	for _, name := range projectedNames(identities) {
		if errorValue := remove(name); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func projectedNames(identities []systemIdentity) []string {
	names := []string{}
	for _, identity := range identities {
		if security.IsProjectedLinuxName(identity.name) {
			names = append(names, identity.name)
		}
	}
	return names
}
