package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type POSIXSynchronizer struct {
	terminalConfiguration config.TerminalConfiguration
	policyPath            string
}

func NewPOSIXSynchronizer(terminalConfiguration config.TerminalConfiguration, policyPath string) POSIXSynchronizer {
	return POSIXSynchronizer{
		terminalConfiguration: terminalConfiguration,
		policyPath:            policyPath,
	}
}

type POSIXRequesterWorkspaceProvisioner struct {
	synchronizer POSIXSynchronizer
}

func NewPOSIXRequesterWorkspaceProvisioner(synchronizer POSIXSynchronizer) POSIXRequesterWorkspaceProvisioner {
	return POSIXRequesterWorkspaceProvisioner{synchronizer: synchronizer}
}

func (provisioner POSIXRequesterWorkspaceProvisioner) ProvisionRequesterWorkspace(ctx context.Context, personAccess policy.PersonAccess, workspaceRootPath string) error {
	personID := strings.TrimSpace(personAccess.PersonID)
	if personID == "" || strings.TrimSpace(provisioner.synchronizer.terminalConfiguration.POSIXHelperPath) == "" {
		return nil
	}
	if errorValue := provisioner.synchronizer.SynchronizeRequester(ctx, personAccess); errorValue != nil {
		return errors.New("requester POSIX workspace provisioning failed for " + personID + ": " + errorValue.Error())
	}
	if errorValue := provisioner.synchronizer.ReconcileHome(ctx, personID, workspaceRootPath); errorValue != nil {
		return errors.New("requester POSIX home reconciliation failed for " + personID + ": " + errorValue.Error())
	}
	return nil
}

func (synchronizer POSIXSynchronizer) Synchronize(ctx context.Context) error {
	helperPath := strings.TrimSpace(synchronizer.terminalConfiguration.POSIXHelperPath)
	if helperPath == "" {
		return nil
	}
	if strings.TrimSpace(synchronizer.policyPath) == "" {
		return errors.New("policy path is required for POSIX synchronization")
	}
	workspaceRootPath := strings.TrimSpace(synchronizer.terminalConfiguration.WorkspaceRootPath)
	if workspaceRootPath == "" {
		workspaceRootPath = "/workspace"
	}
	statePath, cleanupStateDocument, errorValue := synchronizer.createStateDocument(workspaceRootPath, nil)
	if errorValue != nil {
		return errorValue
	}
	defer cleanupStateDocument()

	return synchronizer.runSynchronizeCommand(ctx, statePath, workspaceRootPath)
}

func (synchronizer POSIXSynchronizer) SynchronizeRequester(ctx context.Context, personAccess policy.PersonAccess) error {
	helperPath := strings.TrimSpace(synchronizer.terminalConfiguration.POSIXHelperPath)
	if helperPath == "" {
		return nil
	}
	if strings.TrimSpace(synchronizer.policyPath) == "" {
		return errors.New("policy path is required for POSIX synchronization")
	}
	workspaceRootPath := strings.TrimSpace(synchronizer.terminalConfiguration.WorkspaceRootPath)
	if workspaceRootPath == "" {
		workspaceRootPath = "/workspace"
	}
	statePath, cleanupStateDocument, errorValue := synchronizer.createStateDocument(workspaceRootPath, []policy.PersonAccess{personAccess})
	if errorValue != nil {
		return errorValue
	}
	defer cleanupStateDocument()

	return synchronizer.runSynchronizeCommand(ctx, statePath, workspaceRootPath)
}

func (synchronizer POSIXSynchronizer) runSynchronizeCommand(ctx context.Context, statePath string, workspaceRootPath string) error {
	helperPath := strings.TrimSpace(synchronizer.terminalConfiguration.POSIXHelperPath)
	command := exec.CommandContext(ctx, helperPath, "sync", "--state", statePath, "--workspace", workspaceRootPath)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		return formatPOSIXCommandError("synchronization", helperPath, errorValue, output)
	}
	return nil
}

func (synchronizer POSIXSynchronizer) createStateDocument(workspaceRootPath string, requesterAccesses []policy.PersonAccess) (string, func(), error) {
	policyDocument, errorValue := synchronizer.loadPolicyDocument()
	if errorValue != nil {
		return "", func() {}, errorValue
	}
	policyDocument = policyDocumentWithPOSIXRequesterAccesses(policyDocument, requesterAccesses, workspaceRootPath)
	document, errorValue := json.Marshal(POSIXStateForPolicy(policyDocument, workspaceRootPath))
	if errorValue != nil {
		return "", func() {}, errorValue
	}
	return writeTemporaryPOSIXStateDocument(document)
}

func policyDocumentWithPOSIXRequesterAccesses(policyDocument policy.PolicyDocument, requesterAccesses []policy.PersonAccess, workspaceRootPath string) policy.PolicyDocument {
	result := policyDocument
	for _, requesterAccess := range requesterAccesses {
		result = policyDocumentWithPOSIXRequesterAccess(result, requesterAccess, workspaceRootPath)
	}
	return result
}

func policyDocumentWithPOSIXRequesterAccess(policyDocument policy.PolicyDocument, personAccess policy.PersonAccess, workspaceRootPath string) policy.PolicyDocument {
	personAccess = policy.EnsureRequesterDefaults(personAccess)
	personID := strings.TrimSpace(personAccess.PersonID)
	if personID == "" {
		return policyDocument
	}
	circleIDs := posixCircleIDsForPersonAccess(personAccess)
	policyDocument.Circles = circlePoliciesWithRequesterCircles(policyDocument.Circles, circleIDs, workspaceRootPath)
	for index, personPolicy := range policyDocument.People {
		if strings.TrimSpace(personPolicy.PersonID) != personID {
			continue
		}
		policyDocument.People[index].Circles = uniqueStrings(append(personPolicy.Circles, circleIDs...))
		return policyDocument
	}
	policyDocument.People = append(policyDocument.People, policy.PersonPolicy{
		PersonID: personID,
		Circles:  circleIDs,
	})
	return policyDocument
}

func circlePoliciesWithRequesterCircles(circlePolicies []policy.CirclePolicy, circleIDs []string, workspaceRootPath string) []policy.CirclePolicy {
	result := append([]policy.CirclePolicy{}, circlePolicies...)
	for _, circleID := range circleIDs {
		if circleID == policy.AdminCircleID || hasCirclePolicyID(result, circleID) {
			continue
		}
		result = append(result, policy.CirclePolicy{
			CircleID:               circleID,
			WorkspaceDirectoryPath: strings.TrimRight(workspaceRootPath, "/") + "/circles/" + circleID,
		})
	}
	return result
}

func posixCircleIDsForPersonAccess(personAccess policy.PersonAccess) []string {
	circleIDs := []string{}
	for _, circleID := range personAccess.Circles {
		normalizedCircleID := strings.ToLower(strings.TrimSpace(circleID))
		if normalizedCircleID == "" || normalizedCircleID == policy.AdminCircleID {
			continue
		}
		circleIDs = append(circleIDs, normalizedCircleID)
	}
	return uniqueStrings(circleIDs)
}

func hasCirclePolicyID(circlePolicies []policy.CirclePolicy, circleID string) bool {
	for _, circlePolicy := range circlePolicies {
		if strings.ToLower(strings.TrimSpace(circlePolicy.CircleID)) == circleID {
			return true
		}
	}
	return false
}

func (synchronizer POSIXSynchronizer) loadPolicyDocument() (policy.PolicyDocument, error) {
	document, errorValue := os.ReadFile(synchronizer.policyPath)
	if errorValue != nil {
		return policy.PolicyDocument{}, errorValue
	}
	var policyDocument policy.PolicyDocument
	if errorValue := json.Unmarshal(document, &policyDocument); errorValue != nil {
		return policy.PolicyDocument{}, errorValue
	}
	return policyDocument, nil
}

func writeTemporaryPOSIXStateDocument(document []byte) (string, func(), error) {
	file, errorValue := os.CreateTemp("", "blueclaw-posix-state-*.json")
	if errorValue != nil {
		return "", func() {}, errorValue
	}
	statePath := file.Name()
	cleanup := func() {
		_ = os.Remove(statePath)
	}
	if _, errorValue := file.Write(document); errorValue != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, errorValue
	}
	if errorValue := file.Close(); errorValue != nil {
		cleanup()
		return "", func() {}, errorValue
	}
	return statePath, cleanup, nil
}

func (synchronizer POSIXSynchronizer) ReconcileHome(ctx context.Context, personID string, workspaceRootPath string) error {
	helperPath := strings.TrimSpace(synchronizer.terminalConfiguration.POSIXHelperPath)
	if helperPath == "" {
		return nil
	}
	rootPath := strings.TrimSpace(workspaceRootPath)
	if rootPath == "" {
		rootPath = strings.TrimSpace(synchronizer.terminalConfiguration.WorkspaceRootPath)
	}
	if rootPath == "" {
		rootPath = "/workspace"
	}
	command := exec.CommandContext(ctx, helperPath, "reconcile-home", "--person-id", strings.TrimSpace(personID), "--workspace", rootPath)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		return formatPOSIXCommandError("home reconciliation", helperPath, errorValue, output)
	}
	return nil
}

func formatPOSIXCommandError(operation string, helperPath string, errorValue error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = "helper produced no output"
	}
	return fmt.Errorf("POSIX %s failed using helper %q: %w; output: %s", operation, helperPath, errorValue, detail)
}
