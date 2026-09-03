package identity

import (
	"strings"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type PlatformAccountRepository interface {
	UpsertPlatformAccount(PlatformAccountIdentity) error
	ListPlatformAccount() ([]PlatformAccountIdentity, error)
}

type IdentityService struct {
	mutex                        sync.RWMutex
	personIDByEmail              map[string]string
	emailByPersonID              map[string]string
	displayNameByPersonID        map[string]string
	personAccessByPersonID       map[string]policy.PersonAccess
	channelByCompositeKey        map[string]policy.ChannelPolicy
	personIDByPlatformAccountKey map[string]string
	containedCirclesByID         map[string][]string
	platformAccountRepository    PlatformAccountRepository
}

func NewIdentityService(policyProjection policy.PolicyProjection) *IdentityService {
	identityService := &IdentityService{
		personIDByEmail:              map[string]string{},
		emailByPersonID:              map[string]string{},
		displayNameByPersonID:        map[string]string{},
		personAccessByPersonID:       map[string]policy.PersonAccess{},
		channelByCompositeKey:        map[string]policy.ChannelPolicy{},
		personIDByPlatformAccountKey: map[string]string{},
	}

	identityService.reloadPolicyProjection(policyProjection)

	return identityService
}

func (identityService *IdentityService) UsePlatformAccountRepository(repository PlatformAccountRepository) {
	identityService.mutex.Lock()
	defer identityService.mutex.Unlock()
	identityService.platformAccountRepository = repository
	identityService.reloadPlatformAccounts()
}

func (identityService *IdentityService) ReloadPolicyProjection(policyProjection policy.PolicyProjection) {
	identityService.mutex.Lock()
	defer identityService.mutex.Unlock()
	identityService.reloadPolicyProjection(policyProjection)
	identityService.reloadPlatformAccounts()
}

func (identityService *IdentityService) reloadPolicyProjection(policyProjection policy.PolicyProjection) {
	identityService.personIDByEmail = map[string]string{}
	identityService.emailByPersonID = map[string]string{}
	identityService.displayNameByPersonID = map[string]string{}
	identityService.personAccessByPersonID = map[string]policy.PersonAccess{}
	identityService.channelByCompositeKey = map[string]policy.ChannelPolicy{}
	identityService.personIDByPlatformAccountKey = map[string]string{}
	identityService.containedCirclesByID = map[string][]string{}
	for circleID, memberCircles := range policyProjection.ContainedCirclesByID {
		identityService.containedCirclesByID[circleID] = append([]string{}, memberCircles...)
	}
	for email, personID := range policyProjection.PersonIDByEmail {
		normalizedEmail := strings.ToLower(strings.TrimSpace(email))
		identityService.personIDByEmail[normalizedEmail] = personID
		if identityService.emailByPersonID[personID] == "" {
			identityService.emailByPersonID[personID] = normalizedEmail
		}
	}
	for personID, displayName := range policyProjection.DisplayNameByPersonID {
		identityService.displayNameByPersonID[personID] = displayName
	}
	for personID, personAccess := range policyProjection.PersonAccessByPersonID {
		identityService.personAccessByPersonID[personID] = policy.PersonAccess{
			PersonID:            personAccess.PersonID,
			Circles:             append([]string{}, personAccess.Circles...),
			ResourceAccessRules: append([]policy.ResourceAccessPolicy{}, personAccess.ResourceAccessRules...),
			SecurityLevelRank:   personAccess.SecurityLevelRank,
			GrantedClasses:      append([]string{}, personAccess.GrantedClasses...),
		}
	}
	for compositeKey, channelPolicy := range policyProjection.ChannelByCompositeKey {
		identityService.channelByCompositeKey[compositeKey] = channelPolicy
	}
}

func (identityService *IdentityService) ResolvePersonIDByEmail(email string) (string, bool) {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	personID, isFound := identityService.personIDByEmail[strings.ToLower(strings.TrimSpace(email))]
	return personID, isFound
}

func (identityService *IdentityService) ResolvePersonIDByPlatformAccount(platform string, externalUserID string) (string, bool) {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	personID, isFound := identityService.personIDByPlatformAccountKey[platform+":"+externalUserID]
	return personID, isFound
}

func (identityService *IdentityService) IsApprovedInternalEmail(email string) bool {
	_, isFound := identityService.ResolvePersonIDByEmail(email)
	return isFound
}

func (identityService *IdentityService) ResolvePersonAccess(personID string) policy.PersonAccess {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	personAccess, isFound := identityService.personAccessByPersonID[personID]
	if !isFound {
		return policy.EnsureRequesterDefaults(policy.PersonAccess{PersonID: personID})
	}
	personAccess.Circles = append([]string{}, personAccess.Circles...)
	personAccess.ResourceAccessRules = append([]policy.ResourceAccessPolicy{}, personAccess.ResourceAccessRules...)
	personAccess.GrantedClasses = append([]string{}, personAccess.GrantedClasses...)
	return policy.EnsureRequesterDefaults(personAccess)
}

func (identityService *IdentityService) ContainedCircles() map[string][]string {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	contained := make(map[string][]string, len(identityService.containedCirclesByID))
	for circleID, memberCircles := range identityService.containedCirclesByID {
		contained[circleID] = append([]string{}, memberCircles...)
	}
	return contained
}

func (identityService *IdentityService) ResolvePersonPrimaryEmail(personID string) string {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	return identityService.emailByPersonID[strings.TrimSpace(personID)]
}

func (identityService *IdentityService) ResolvePersonIDByDisplayName(displayName string) (string, bool) {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	wanted := strings.TrimSpace(displayName)
	if wanted == "" {
		return "", false
	}
	matchedPersonID := ""
	for personID, candidate := range identityService.displayNameByPersonID {
		if !strings.EqualFold(strings.TrimSpace(candidate), wanted) {
			continue
		}
		if matchedPersonID != "" {
			return "", false
		}
		matchedPersonID = personID
	}
	return matchedPersonID, matchedPersonID != ""
}

func (identityService *IdentityService) ResolvePersonDisplayName(personID string) string {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	return identityService.displayNameByPersonID[strings.TrimSpace(personID)]
}

func (identityService *IdentityService) ResolveConversationPolicy(platform string, conversationID string) (policy.ChannelPolicy, bool) {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	channelPolicy, isFound := identityService.channelByCompositeKey[platform+":"+conversationID]
	return channelPolicy, isFound
}

func (identityService *IdentityService) RememberPlatformAccount(platformAccountIdentity PlatformAccountIdentity) {
	identityService.mutex.Lock()
	defer identityService.mutex.Unlock()

	normalizedEmail := strings.ToLower(strings.TrimSpace(platformAccountIdentity.Email))
	personID, isFound := identityService.personIDByEmail[normalizedEmail]
	if !isFound {
		return
	}

	identityService.personIDByPlatformAccountKey[platformAccountIdentity.Platform+":"+platformAccountIdentity.ExternalUserID] = personID
	platformAccountIdentity.PersonID = personID
	_ = identityService.savePlatformAccount(platformAccountIdentity)
}

func (identityService *IdentityService) reloadPlatformAccounts() {
	if identityService.platformAccountRepository == nil {
		return
	}
	platformAccounts, errorValue := identityService.platformAccountRepository.ListPlatformAccount()
	if errorValue != nil {
		return
	}
	for _, platformAccount := range platformAccounts {
		personID := identityService.currentPolicyPersonID(platformAccount)
		if personID == "" {
			continue
		}
		identityService.personIDByPlatformAccountKey[platformAccount.Platform+":"+platformAccount.ExternalUserID] = personID
	}
}

func (identityService *IdentityService) currentPolicyPersonID(platformAccount PlatformAccountIdentity) string {
	emailPersonID := ""
	if platformAccount.Email != "" {
		emailPersonID = identityService.personIDByEmail[strings.ToLower(strings.TrimSpace(platformAccount.Email))]
	}
	if emailPersonID != "" {
		return emailPersonID
	}
	if _, isFound := identityService.personAccessByPersonID[platformAccount.PersonID]; isFound {
		return platformAccount.PersonID
	}
	return ""
}

func (identityService *IdentityService) savePlatformAccount(platformAccountIdentity PlatformAccountIdentity) error {
	if identityService.platformAccountRepository == nil {
		return nil
	}
	return identityService.platformAccountRepository.UpsertPlatformAccount(platformAccountIdentity)
}
