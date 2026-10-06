package identity

import "sort"

func DirectMessageAccount(personID string, accounts []PlatformAccountIdentity) (PlatformAccountIdentity, bool) {
	matching := make([]PlatformAccountIdentity, 0)
	for _, account := range accounts {
		if account.PersonID == personID && account.ExternalUserID != "" && account.Platform != "" && account.Platform != "api" {
			matching = append(matching, account)
		}
	}
	sort.Slice(matching, func(first int, second int) bool {
		if matching[first].Platform != matching[second].Platform {
			return matching[first].Platform < matching[second].Platform
		}
		return matching[first].ExternalUserID < matching[second].ExternalUserID
	})
	if len(matching) == 0 {
		return PlatformAccountIdentity{}, false
	}
	if len(matching) > 1 && matching[0].Platform == matching[1].Platform && matching[0].ExternalUserID != matching[1].ExternalUserID {
		return PlatformAccountIdentity{}, false
	}
	return matching[0], true
}
