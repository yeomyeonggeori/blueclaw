package memory

import (
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type Reader struct {
	PersonID          string
	CircleIDs         []string
	SecurityLevelRank int
	GrantedClasses    []string
}

func ReaderFromPersonAccess(personAccess policy.PersonAccess) Reader {
	return Reader{
		PersonID:          strings.TrimSpace(personAccess.PersonID),
		CircleIDs:         append([]string{}, personAccess.Circles...),
		SecurityLevelRank: personAccess.SecurityLevelRank,
		GrantedClasses:    append([]string{}, personAccess.GrantedClasses...),
	}
}

func (reader Reader) CanRead(fact Fact) bool {
	if !reader.isInScope(fact) {
		return false
	}
	if fact.SecurityLevelRank > reader.SecurityLevelRank {
		return false
	}
	for _, requiredClass := range fact.RequiredClasses {
		if !containsString(reader.GrantedClasses, requiredClass) {
			return false
		}
	}
	return true
}

func (reader Reader) isInScope(fact Fact) bool {
	switch fact.ScopeType {
	case ScopeTypePrivate:
		return reader.PersonID != "" && fact.ScopeID == reader.PersonID
	case ScopeTypeCircle:
		return containsString(reader.CircleIDs, fact.ScopeID)
	case ScopeTypeWorkspace:
		return true
	}
	return false
}
