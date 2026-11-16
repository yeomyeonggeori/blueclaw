package main

import "testing"

const boundMacDirectoryServiceListing = `
nobody                  -2
root                    0
daemon                  1
_www                    70
com.apple.access_remote_ae 400
ADDOMAIN\alice          100000
ADDOMAIN\bob            100001
bc_person_carol_9f2a1c  100002
ADDOMAIN\dave hopper    100003
`

func reservationTableFromListing(listing string) *identityAllocationTable {
	return restoredTableOnCard(map[string]uint32{}, parseDirectoryServiceList(listing))
}

func restoredTableOnCard(restored map[string]uint32, onTheCard []systemIdentity) *identityAllocationTable {
	table := &identityAllocationTable{allocations: restored, reserved: map[uint32]bool{}}
	table.reserve(onTheCard)
	return table
}

func TestANumberTheRestoredMapGivesIsNotReusedWhenAnEarlierCompanysAccountHoldsIt(testInstance *testing.T) {
	restored := map[string]uint32{"bc_person_sample_8545e20f": 100003}
	onTheCard := []systemIdentity{
		{name: "bc_person_earlier_company_77aa", identityID: 100003},
		{name: "bc_person_earlier_company_88bb", identityID: 100004},
	}
	table := restoredTableOnCard(restored, onTheCard)

	identityID := table.idFor("bc_person_sample_8545e20f")

	if identityID == 100003 || identityID == 100004 {
		testInstance.Fatalf("the restored person was given %d, which an account the card already holds owns, so groupadd refuses it", identityID)
	}
	if again := table.idFor("bc_person_sample_8545e20f"); again != identityID {
		testInstance.Fatalf("the same person was given %d and then %d", identityID, again)
	}
	if !table.dirty {
		testInstance.Fatal("the new number is not written back, so the next sync would try the taken one again")
	}
}

func TestARestoredNumberThatTheSameAccountHoldsIsKept(testInstance *testing.T) {
	restored := map[string]uint32{"bc_person_sample_8545e20f": 100003}
	onTheCard := []systemIdentity{{name: "bc_person_sample_8545e20f", identityID: 100003}}
	table := restoredTableOnCard(restored, onTheCard)

	if identityID := table.idFor("bc_person_sample_8545e20f"); identityID != 100003 {
		testInstance.Fatalf("an account that already holds its restored number was moved to %d", identityID)
	}
}

func TestTheAllocatorNeverHandsOutAnIdentityDirectoryServiceAlreadyReports(testInstance *testing.T) {
	table := reservationTableFromListing(boundMacDirectoryServiceListing)
	alreadyReported := map[uint32]bool{}
	for identityID := range table.reserved {
		alreadyReported[identityID] = true
	}

	for _, name := range []string{"bc_person_eve_33aa", "bc_person_frank_44bb", "bc_circle_finance_55cc"} {
		identityID := table.idFor(name)
		if alreadyReported[identityID] {
			testInstance.Fatalf("%s was allocated %d, which a mobile account on a bound Mac already holds", name, identityID)
		}
		if identityID < posixIdentityBaseID {
			testInstance.Fatalf("%s was allocated %d, below the projection base %d", name, identityID, posixIdentityBaseID)
		}
	}
}

func TestAProjectedAccountAboveTheBaseIsRecoveredRatherThanReallocated(testInstance *testing.T) {
	table := reservationTableFromListing(boundMacDirectoryServiceListing)

	if identityID := table.idFor("bc_person_carol_9f2a1c"); identityID != 100002 {
		testInstance.Fatalf("the existing projected account was given %d instead of the 100002 Directory Service reports", identityID)
	}
}

func TestADirectoryServiceNameWithSpacesKeepsItsIdentity(testInstance *testing.T) {
	identities := parseDirectoryServiceList(boundMacDirectoryServiceListing)

	for _, identity := range identities {
		if identity.name == `ADDOMAIN\dave hopper` {
			if identity.identityID != 100003 {
				testInstance.Fatalf("expected 100003 for a name containing a space, got %d", identity.identityID)
			}
			return
		}
	}
	testInstance.Fatal("a Directory Service name containing a space was dropped, so its identity would not be reserved")
}
