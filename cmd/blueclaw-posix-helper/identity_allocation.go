package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const posixIdentityBaseID = 100000

type identityAllocationTable struct {
	filePath    string
	allocations map[string]uint32
	reserved    map[uint32]bool
	holders     map[uint32]map[string]bool
	dirty       bool
}

func loadIdentityAllocationTable(workspacePath string) (*identityAllocationTable, error) {
	table := &identityAllocationTable{
		filePath:    filepath.Join(workspacePath, ".blueclaw", "identity-map.json"),
		allocations: map[string]uint32{},
		reserved:    map[uint32]bool{},
		holders:     map[uint32]map[string]bool{},
	}
	document, errorValue := os.ReadFile(table.filePath)
	if errorValue != nil && !errors.Is(errorValue, os.ErrNotExist) {
		return nil, errorValue
	}
	if errorValue == nil && len(bytes.TrimSpace(document)) > 0 {
		if errorValue := json.Unmarshal(document, &table.allocations); errorValue != nil {
			return nil, errorValue
		}
	}
	if errorValue := table.reserveSystemIdentities(); errorValue != nil {
		return nil, errorValue
	}
	return table, nil
}

type systemIdentity struct {
	name       string
	identityID uint32
}

func (table *identityAllocationTable) reserveSystemIdentities() error {
	groups, errorValue := readSystemGroups()
	if errorValue != nil {
		return errorValue
	}
	table.reserve(groups)
	users, errorValue := readSystemUsers()
	if errorValue != nil {
		return errorValue
	}
	table.reserve(users)
	return nil
}

func (table *identityAllocationTable) reserve(identities []systemIdentity) {
	for _, identity := range identities {
		table.reserved[identity.identityID] = true
		table.noteHolder(identity)
		table.recoverProjectedAllocation(identity)
	}
}

func (table *identityAllocationTable) noteHolder(identity systemIdentity) {
	if table.holders == nil {
		table.holders = map[uint32]map[string]bool{}
	}
	if table.holders[identity.identityID] == nil {
		table.holders[identity.identityID] = map[string]bool{}
	}
	table.holders[identity.identityID][identity.name] = true
}

func (table *identityAllocationTable) isHeldByAnotherName(identityID uint32, name string) bool {
	holders := table.holders[identityID]
	return len(holders) > 0 && !holders[name]
}

func (table *identityAllocationTable) recoverProjectedAllocation(identity systemIdentity) {
	if identity.identityID < posixIdentityBaseID || !strings.HasPrefix(identity.name, "bc_") {
		return
	}
	if _, isAllocated := table.allocations[identity.name]; isAllocated {
		return
	}
	table.allocations[identity.name] = identity.identityID
	table.dirty = true
}

func (table *identityAllocationTable) idFor(name string) uint32 {
	if identityID, isAllocated := table.allocations[name]; isAllocated && !table.isHeldByAnotherName(identityID, name) {
		return identityID
	}
	identityID := table.nextFreeID()
	table.allocations[name] = identityID
	table.reserved[identityID] = true
	table.dirty = true
	return identityID
}

func (table *identityAllocationTable) nextFreeID() uint32 {
	candidate := uint32(posixIdentityBaseID)
	for _, identityID := range table.allocations {
		if identityID >= candidate {
			candidate = identityID + 1
		}
	}
	for table.reserved[candidate] {
		candidate++
	}
	return candidate
}

func (table *identityAllocationTable) persist() error {
	if !table.dirty {
		return nil
	}
	if errorValue := os.MkdirAll(filepath.Dir(table.filePath), 0700); errorValue != nil {
		return errorValue
	}
	document, errorValue := json.MarshalIndent(table.allocations, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	return os.WriteFile(table.filePath, document, 0600)
}
