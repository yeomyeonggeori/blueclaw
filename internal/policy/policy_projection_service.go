package policy

import "strings"

const (
	MemberCircleID = "member"
	AdminCircleID  = "admin"
)

type PolicyProjection struct {
	ApprovedEmailByPersonID map[string][]string
	PersonIDByEmail         map[string]string
	DisplayNameByPersonID   map[string]string
	PersonAccessByPersonID  map[string]PersonAccess
	ChannelByCompositeKey   map[string]ChannelPolicy
	ResourceAccessRules     []ResourceAccessPolicy
}

type PersonAccess struct {
	PersonID            string
	Circles             []string
	ResourceAccessRules []ResourceAccessPolicy
	SecurityLevelRank   int
	GrantedClasses      []string
}

type PolicyProjectionService struct{}

func (policyProjectionService PolicyProjectionService) ReplacePolicyProjectionTransactionally(policyDocument PolicyDocument) PolicyProjection {
	policyProjection := PolicyProjection{
		ApprovedEmailByPersonID: map[string][]string{},
		PersonIDByEmail:         map[string]string{},
		DisplayNameByPersonID:   map[string]string{},
		PersonAccessByPersonID:  map[string]PersonAccess{},
		ChannelByCompositeKey:   map[string]ChannelPolicy{},
		ResourceAccessRules:     append([]ResourceAccessPolicy{}, policyDocument.ResourceAccess...),
	}

	for _, personPolicy := range policyDocument.People {
		policyProjection.ApprovedEmailByPersonID[personPolicy.PersonID] = append([]string{}, personPolicy.Emails...)
		if displayName := strings.TrimSpace(personPolicy.DisplayName); displayName != "" {
			policyProjection.DisplayNameByPersonID[personPolicy.PersonID] = displayName
		}
		policyProjection.PersonAccessByPersonID[personPolicy.PersonID] = PersonAccess{
			PersonID:            personPolicy.PersonID,
			Circles:             effectivePersonCircles(personPolicy),
			ResourceAccessRules: append([]ResourceAccessPolicy{}, policyDocument.ResourceAccess...),
			SecurityLevelRank:   personPolicy.SecurityLevelRank,
			GrantedClasses:      append([]string{}, personPolicy.GrantedClasses...),
		}
		for _, email := range personPolicy.Emails {
			policyProjection.PersonIDByEmail[strings.ToLower(email)] = personPolicy.PersonID
		}
	}

	for _, channelPolicy := range policyDocument.Channels {
		policyProjection.ChannelByCompositeKey[channelPolicy.Platform+":"+channelPolicy.ExternalConversationID] = channelPolicy
	}

	return policyProjection
}

func effectivePersonCircles(personPolicy PersonPolicy) []string {
	circles := append([]string{MemberCircleID}, personPolicy.Circles...)
	if personPolicy.IsAdmin {
		circles = append(circles, AdminCircleID)
	}
	return normalizePolicyStrings(circles)
}

func EnsureRequesterDefaults(personAccess PersonAccess) PersonAccess {
	personAccess.Circles = normalizePolicyStrings(append([]string{MemberCircleID}, personAccess.Circles...))
	return personAccess
}

func CanonicalizePolicyDocument(policyDocument PolicyDocument) PolicyDocument {
	policyDocument.Circles = canonicalCirclePolicies(policyDocument.Circles)
	for index := range policyDocument.People {
		policyDocument.People[index].Circles = effectivePersonCircles(policyDocument.People[index])
	}
	return policyDocument
}

func canonicalCirclePolicies(circlePolicies []CirclePolicy) []CirclePolicy {
	result := append([]CirclePolicy{}, circlePolicies...)
	for index := range result {
		result[index].CircleID = strings.ToLower(strings.TrimSpace(result[index].CircleID))
	}
	if hasCirclePolicy(result, MemberCircleID) {
		return result
	}
	return append([]CirclePolicy{{
		CircleID:               MemberCircleID,
		DisplayName:            "Member",
		WorkspaceDirectoryPath: "/workspace/circles/" + MemberCircleID,
	}}, result...)
}

func hasCirclePolicy(circlePolicies []CirclePolicy, circleID string) bool {
	for _, circlePolicy := range circlePolicies {
		if strings.ToLower(strings.TrimSpace(circlePolicy.CircleID)) == circleID {
			return true
		}
	}
	return false
}

func normalizePolicyStrings(values []string) []string {
	seenValue := map[string]bool{}
	normalizedValues := []string{}
	for _, value := range values {
		normalizedValue := strings.ToLower(strings.TrimSpace(value))
		if normalizedValue == "" || seenValue[normalizedValue] {
			continue
		}
		seenValue[normalizedValue] = true
		normalizedValues = append(normalizedValues, normalizedValue)
	}
	return normalizedValues
}
