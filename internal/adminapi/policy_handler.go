package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type PolicyHandler struct {
	PolicyPath                   string
	PolicyLoader                 policy.PolicyLoader
	PolicySaver                  policy.PolicySaver
	PolicyWatcher                *policy.PolicyWatcher
	Validator                    policy.PolicyValidator
	AuditHandler                 *AuditHandler
	PersonReferenceCanonicalizer PersonReferenceCanonicalizer
	PlatformAccountLinker        PlatformAccountLinker
	OnPolicyReload               func(policy.PolicyDocument)
}

type PersonReferenceCanonicalizer interface {
	CanonicalizePersonReferences(legacyPersonID string, personID string) error
}

type PlatformAccountLinker interface {
	RememberPlatformAccount(platformAccountIdentity identity.PlatformAccountIdentity)
}

type invitePersonRequest struct {
	PersonID          string   `json:"personID"`
	Email             string   `json:"email"`
	DisplayName       string   `json:"displayName"`
	IsAdmin           bool     `json:"isAdmin"`
	Circles           []string `json:"circles"`
	SecurityLevelName string   `json:"securityLevelName"`
	SecurityLevelRank int      `json:"securityLevelRank"`
	GrantedClasses    []string `json:"grantedClasses"`
}

type canonicalizePersonReferencesRequest struct {
	LegacyPersonID string `json:"legacyPersonID"`
	PersonID       string `json:"personID"`
}

func (policyHandler PolicyHandler) HandleGetPolicy(responseWriter http.ResponseWriter, request *http.Request) {
	policyDocument, errorValue := policyHandler.PolicyLoader.LoadPolicyDocument(policyHandler.PolicyPath)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(responseWriter, http.StatusOK, policy.CanonicalizePolicyDocument(policyDocument))
}

func (policyHandler PolicyHandler) HandleValidatePolicy(responseWriter http.ResponseWriter, request *http.Request) {
	var policyDocument policy.PolicyDocument
	errorValue := json.NewDecoder(request.Body).Decode(&policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	policyDocument = policy.CanonicalizePolicyDocument(policyDocument)
	policyDocument = policy.CanonicalizePolicyDocument(policyDocument)
	errorValue = policyHandler.Validator.ValidatePolicyDocument(policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(responseWriter, http.StatusOK, map[string]bool{"isValid": true})
}

func (policyHandler PolicyHandler) HandleSavePolicy(responseWriter http.ResponseWriter, request *http.Request) {
	var policyDocument policy.PolicyDocument
	errorValue := json.NewDecoder(request.Body).Decode(&policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	errorValue = policyHandler.Validator.ValidatePolicyDocument(policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	backupPath, _ := policyHandler.PolicySaver.BackupPolicyDocument(policyHandler.PolicyPath)
	errorValue = policyHandler.PolicySaver.SavePolicyDocumentAtomically(policyHandler.PolicyPath, policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}

	policyHandler.PolicyWatcher.ReloadPolicyDocument(policyDocument)
	policyHandler.notifyPolicyReload(policyDocument)
	policyHandler.AuditHandler.RecordPolicySave(backupPath)
	writeJSON(responseWriter, http.StatusOK, map[string]string{"backupPath": backupPath})
}

// HandleReloadPolicy re-reads the policy file this agent was given and makes it current.
// Where that file comes from is the host's business: on a device it arrives on a delivery
// share the guest cannot write, which is why an agent that must never author its own roster
// still has to be told when the roster changed.
// loadPolicySettled reads the roster, and reads it again if the first attempt caught the
// file mid-write. The host rewrites this file in place so the guest sees the change at all,
// which leaves a window where a reader sees half of it. Refusing on that window would leave
// the agent on its previous roster until somebody asked again.
func (policyHandler PolicyHandler) loadPolicySettled() (policy.PolicyDocument, error) {
	policyDocument, errorValue := policyHandler.PolicyLoader.LoadPolicyDocument(policyHandler.PolicyPath)
	if errorValue == nil {
		return policyDocument, nil
	}
	time.Sleep(policyWriteSettleDelay)
	return policyHandler.PolicyLoader.LoadPolicyDocument(policyHandler.PolicyPath)
}

// policyWriteSettleDelay is how long a torn read waits before looking again. One rewrite of
// a small file finishes far inside it, and a reload is never on a hot path.
const policyWriteSettleDelay = 150 * time.Millisecond

func (policyHandler PolicyHandler) HandleReloadPolicy(responseWriter http.ResponseWriter, request *http.Request) {
	policyDocument, errorValue := policyHandler.loadPolicySettled()
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}

	policyHandler.PolicyWatcher.ReloadPolicyDocument(policyDocument)
	policyHandler.notifyPolicyReload(policyDocument)
	writeJSON(responseWriter, http.StatusOK, map[string]int{"people": len(policyDocument.People)})
}

func (policyHandler PolicyHandler) HandleInvitePerson(responseWriter http.ResponseWriter, request *http.Request) {
	var inviteRequest invitePersonRequest
	errorValue := json.NewDecoder(request.Body).Decode(&inviteRequest)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	policyDocument, personPolicy, backupPath, errorValue := policyHandler.invitePerson(inviteRequest)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), statusCodeForPolicyMutationError(errorValue))
		return
	}

	policyHandler.PolicyWatcher.ReloadPolicyDocument(policyDocument)
	policyHandler.notifyPolicyReload(policyDocument)
	policyHandler.linkInvitedPersonMattermostAccount(personPolicy)
	if backupPath != "" {
		policyHandler.AuditHandler.RecordPolicySave(backupPath)
	}
	writeJSON(responseWriter, http.StatusOK, personPolicy)
}

// An invited workspace person carries their Mattermost user ID as the personID, so
// link that platform account immediately. Without this the person resolves by name
// but stays unlinked until Blueclaw happens to ingest a message from them, which
// would make a freshly invited member unreachable by direct message.
func (policyHandler PolicyHandler) linkInvitedPersonMattermostAccount(personPolicy policy.PersonPolicy) {
	if policyHandler.PlatformAccountLinker == nil {
		return
	}
	externalUserID := strings.TrimSpace(personPolicy.PersonID)
	email := firstPersonEmail(personPolicy)
	if externalUserID == "" || email == "" {
		return
	}
	policyHandler.PlatformAccountLinker.RememberPlatformAccount(identity.PlatformAccountIdentity{
		Platform:       "mattermost",
		ExternalUserID: externalUserID,
		Email:          email,
		DisplayName:    strings.TrimSpace(personPolicy.DisplayName),
		PersonID:       externalUserID,
	})
}

func firstPersonEmail(personPolicy policy.PersonPolicy) string {
	for _, email := range personPolicy.Emails {
		trimmedEmail := strings.TrimSpace(email)
		if trimmedEmail != "" {
			return trimmedEmail
		}
	}
	return ""
}

func (policyHandler PolicyHandler) HandleRemovePerson(responseWriter http.ResponseWriter, request *http.Request) {
	email := normalizeEmail(request.URL.Query().Get("email"))
	personID := strings.TrimSpace(request.URL.Query().Get("personID"))
	policyDocument, backupPath, errorValue := policyHandler.removePerson(email, personID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), statusCodeForPolicyMutationError(errorValue))
		return
	}

	policyHandler.PolicyWatcher.ReloadPolicyDocument(policyDocument)
	policyHandler.notifyPolicyReload(policyDocument)
	if backupPath != "" {
		policyHandler.AuditHandler.RecordPolicySave(backupPath)
	}
	writeJSON(responseWriter, http.StatusOK, map[string]bool{"isRemoved": true})
}

func (policyHandler PolicyHandler) HandleCanonicalizePersonReferences(responseWriter http.ResponseWriter, request *http.Request) {
	if policyHandler.PersonReferenceCanonicalizer == nil {
		http.Error(responseWriter, "person reference canonicalizer is not configured", http.StatusServiceUnavailable)
		return
	}
	var canonicalizeRequest canonicalizePersonReferencesRequest
	errorValue := json.NewDecoder(request.Body).Decode(&canonicalizeRequest)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	legacyPersonID := strings.TrimSpace(canonicalizeRequest.LegacyPersonID)
	personID := strings.TrimSpace(canonicalizeRequest.PersonID)
	if legacyPersonID == "" || personID == "" {
		http.Error(responseWriter, "legacyPersonID and personID are required", http.StatusBadRequest)
		return
	}
	if legacyPersonID == personID {
		writeJSON(responseWriter, http.StatusOK, map[string]bool{"isCanonicalized": false})
		return
	}
	if errorValue := policyHandler.PersonReferenceCanonicalizer.CanonicalizePersonReferences(legacyPersonID, personID); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]bool{"isCanonicalized": true})
}

func (policyHandler PolicyHandler) notifyPolicyReload(policyDocument policy.PolicyDocument) {
	if policyHandler.OnPolicyReload != nil {
		policyHandler.OnPolicyReload(policyDocument)
	}
}

func (policyHandler PolicyHandler) invitePerson(inviteRequest invitePersonRequest) (policy.PolicyDocument, policy.PersonPolicy, string, error) {
	personID := strings.TrimSpace(inviteRequest.PersonID)
	if personID == "" {
		return policy.PolicyDocument{}, policy.PersonPolicy{}, "", errors.New("personID is required")
	}
	email := normalizeEmail(inviteRequest.Email)
	if email == "" {
		return policy.PolicyDocument{}, policy.PersonPolicy{}, "", errors.New("email is required")
	}

	policyDocument, errorValue := policyHandler.PolicyLoader.LoadPolicyDocument(policyHandler.PolicyPath)
	if errorValue != nil {
		return policy.PolicyDocument{}, policy.PersonPolicy{}, "", errorValue
	}

	for index, personPolicy := range policyDocument.People {
		if personPolicy.PersonID == personID && !personHasEmail(personPolicy, email) {
			return policy.PolicyDocument{}, policy.PersonPolicy{}, "", errors.New("personID already belongs to another person")
		}
		if personHasEmail(personPolicy, email) {
			if personPolicy.PersonID == personID {
				return policyDocument, personPolicy, "", nil
			}
			personPolicy.PersonID = personID
			policyDocument.People[index] = personPolicy
			backupPath, errorValue := policyHandler.savePolicyDocument(policyDocument)
			if errorValue != nil {
				return policy.PolicyDocument{}, policy.PersonPolicy{}, "", errorValue
			}
			return policyDocument, personPolicy, backupPath, nil
		}
	}

	personPolicy := createInvitedPersonPolicy(inviteRequest, email)
	policyDocument.People = append(policyDocument.People, personPolicy)
	backupPath, errorValue := policyHandler.savePolicyDocument(policyDocument)
	if errorValue != nil {
		return policy.PolicyDocument{}, policy.PersonPolicy{}, "", errorValue
	}

	return policyDocument, personPolicy, backupPath, nil
}

func (policyHandler PolicyHandler) removePerson(email string, personID string) (policy.PolicyDocument, string, error) {
	if email == "" && personID == "" {
		return policy.PolicyDocument{}, "", errors.New("email or personID is required")
	}

	policyDocument, errorValue := policyHandler.PolicyLoader.LoadPolicyDocument(policyHandler.PolicyPath)
	if errorValue != nil {
		return policy.PolicyDocument{}, "", errorValue
	}

	filteredPeople := make([]policy.PersonPolicy, 0, len(policyDocument.People))
	isRemoved := false
	for _, personPolicy := range policyDocument.People {
		if shouldRemovePerson(personPolicy, email, personID) {
			if personPolicy.IsAdmin {
				return policy.PolicyDocument{}, "", errors.New("cannot remove admin person")
			}
			isRemoved = true
			continue
		}
		filteredPeople = append(filteredPeople, personPolicy)
	}
	if !isRemoved {
		return policy.PolicyDocument{}, "", errors.New("person not found")
	}

	policyDocument.People = filteredPeople
	backupPath, errorValue := policyHandler.savePolicyDocument(policyDocument)
	if errorValue != nil {
		return policy.PolicyDocument{}, "", errorValue
	}

	return policyDocument, backupPath, nil
}

func (policyHandler PolicyHandler) savePolicyDocument(policyDocument policy.PolicyDocument) (string, error) {
	policyDocument = policy.CanonicalizePolicyDocument(policyDocument)
	errorValue := policyHandler.Validator.ValidatePolicyDocument(policyDocument)
	if errorValue != nil {
		return "", errorValue
	}

	backupPath, _ := policyHandler.PolicySaver.BackupPolicyDocument(policyHandler.PolicyPath)
	errorValue = policyHandler.PolicySaver.SavePolicyDocumentAtomically(policyHandler.PolicyPath, policyDocument)
	if errorValue != nil {
		return "", errorValue
	}

	return backupPath, nil
}

func createInvitedPersonPolicy(inviteRequest invitePersonRequest, email string) policy.PersonPolicy {
	securityLevelName := strings.TrimSpace(inviteRequest.SecurityLevelName)
	securityLevelRank := inviteRequest.SecurityLevelRank
	grantedClasses := append([]string{}, inviteRequest.GrantedClasses...)
	circles := normalizeCircles(append([]string{policy.MemberCircleID}, inviteRequest.Circles...))
	if inviteRequest.IsAdmin {
		securityLevelName = "admin"
		securityLevelRank = 100
		grantedClasses = []string{"internal", "executive"}
		circles = normalizeCircles(append(circles, policy.AdminCircleID))
	}
	if securityLevelName == "" {
		securityLevelName = "member"
	}
	if securityLevelRank == 0 {
		securityLevelRank = 10
	}
	if len(grantedClasses) == 0 {
		grantedClasses = []string{"internal"}
	}

	return policy.PersonPolicy{
		PersonID:          strings.TrimSpace(inviteRequest.PersonID),
		DisplayName:       displayNameForInvite(inviteRequest.DisplayName, email),
		Emails:            []string{email},
		Circles:           circles,
		SecurityLevelName: securityLevelName,
		SecurityLevelRank: securityLevelRank,
		GrantedClasses:    grantedClasses,
		IsAdmin:           inviteRequest.IsAdmin,
	}
}

func normalizeCircles(values []string) []string {
	seenValue := map[string]bool{}
	circles := []string{}
	for _, value := range values {
		trimmedValue := strings.ToLower(strings.TrimSpace(value))
		if trimmedValue == "" || seenValue[trimmedValue] {
			continue
		}
		seenValue[trimmedValue] = true
		circles = append(circles, trimmedValue)
	}
	return circles
}

func displayNameForInvite(displayName string, email string) string {
	trimmedDisplayName := strings.TrimSpace(displayName)
	if trimmedDisplayName != "" {
		return trimmedDisplayName
	}

	localPart, _, isEmail := strings.Cut(email, "@")
	if isEmail && localPart != "" {
		return localPart
	}

	return email
}

func personHasEmail(personPolicy policy.PersonPolicy, email string) bool {
	for _, personEmail := range personPolicy.Emails {
		if normalizeEmail(personEmail) == email {
			return true
		}
	}
	return false
}

func shouldRemovePerson(personPolicy policy.PersonPolicy, email string, personID string) bool {
	if personID != "" && personPolicy.PersonID == personID {
		return true
	}
	if email == "" {
		return false
	}
	return personHasEmail(personPolicy, email)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func statusCodeForPolicyMutationError(errorValue error) int {
	switch errorValue.Error() {
	case "personID is required", "email is required", "email or personID is required", "cannot remove admin person", "personID already belongs to another person":
		return http.StatusBadRequest
	case "person not found":
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(responseWriter http.ResponseWriter, statusCode int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	_ = json.NewEncoder(responseWriter).Encode(value)
}
