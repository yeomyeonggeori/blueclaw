//go:build !nobundledharness

package e2e

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const (
	virtualHostUpdateToolName      = "host_update"
	virtualHostInstalledVersion    = "v2099.10.01.203142"
	virtualHostLatestVersion       = "v2099.10.02.090000"
	virtualHostOffHoursStartsAt    = "2099-10-03T03:00:00+09:00"
	virtualHostOffHoursChoiceKey   = "offHours"
	virtualHostNowChoiceKey        = "now"
	virtualHostRequestedChoiceKey  = "requestedTime"
	virtualRequesterPersonID       = "person-1"
	virtualHostUpdateNotAdminError = "access_denied"
)

type virtualInvokeContext struct {
	RequesterPersonID string `json:"requesterPersonID"`
	HoldID            string `json:"holdID"`
	IsScheduledRun    bool   `json:"isScheduledRun"`
}

func virtualInvokeContextOf(requestBody []byte) virtualInvokeContext {
	document := struct {
		Context virtualInvokeContext `json:"context"`
	}{}
	json.Unmarshal(requestBody, &document)
	return document.Context
}

func (invokeContext virtualInvokeContext) carriesAnApproval() bool {
	return strings.TrimSpace(invokeContext.HoldID) != ""
}

func (service *virtualCapabilityService) isAdmin(personID string) bool {
	return service.adminPersonIDs[strings.TrimSpace(personID)]
}

func (service *virtualCapabilityService) hostUpdateTargetResponse(requestBody []byte) string {
	if !service.isAdmin(virtualInvokeContextOf(requestBody).RequesterPersonID) {
		return virtualHostUpdateRefusal("only an administrator of this company can update its host")
	}
	input := virtualCapabilityInput(requestBody)
	return virtualCapabilityTargetResolved(virtualHostUpdateToolName, map[string]any{
		"inputField": "targetVersion",
		"id":         virtualHostLatestVersion,
		"title":      virtualHostInstalledVersion + " → " + virtualHostLatestVersion,
		"preview":    `{"fromVersion":"` + virtualHostInstalledVersion + `","toVersion":"` + virtualHostLatestVersion + `","servicesStopForMinutes":2}`,
		"choices":    virtualHostUpdateChoices(input),
	})
}

func virtualHostUpdateChoices(input map[string]any) []map[string]string {
	now := map[string]string{"key": virtualHostNowChoiceKey}
	if requested := strings.TrimSpace(stringValue(input["startsAt"])); requested != "" {
		return []map[string]string{{"key": virtualHostRequestedChoiceKey, "startsAt": requested}, now}
	}
	offHours := map[string]string{"key": virtualHostOffHoursChoiceKey, "startsAt": virtualHostOffHoursStartsAt}
	if isRequestedNow, _ := input["isRequestedNow"].(bool); isRequestedNow {
		return []map[string]string{now, offHours}
	}
	return []map[string]string{offHours, now}
}

func (service *virtualCapabilityService) hostUpdateResponse(requestBody []byte) string {
	invokeContext := virtualInvokeContextOf(requestBody)
	if !service.isAdmin(invokeContext.RequesterPersonID) {
		return virtualHostUpdateRefusal("only an administrator of this company can update its host")
	}
	if !invokeContext.carriesAnApproval() {
		return virtualCapabilityApprovalRequired(virtualHostUpdateToolName)
	}
	input := virtualCapabilityInput(requestBody)
	return virtualCapabilitySuccess(virtualHostUpdateToolName, map[string]any{
		"status":      "started",
		"fromVersion": virtualHostInstalledVersion,
		"toVersion":   stringValue(input["targetVersion"]),
		"isScheduled": invokeContext.IsScheduledRun,
	})
}

func virtualHostUpdateRefusal(message string) string {
	return virtualCapabilityJSON(map[string]any{
		"provider": "virtual", "selectedBackend": "device", "toolName": virtualHostUpdateToolName,
		"outcome": "denied", "status": "denied", "isError": true,
		"content": message, "message": message, "errorCode": virtualHostUpdateNotAdminError, "failureStage": "authorization",
		"result": map[string]any{"message": message},
	})
}

func virtualPolicyProjection(isRequesterAdmin bool) policy.PolicyProjection {
	policyDocument := testPolicyDocument()
	policyDocument.People[0].IsAdmin = isRequesterAdmin
	return policy.PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(policyDocument)
}

func virtualAdminPersonIDs(scenario VirtualSessionScenario) map[string]bool {
	if !scenario.RequesterIsAdmin {
		return map[string]bool{}
	}
	return map[string]bool{virtualRequesterPersonID: true}
}

type virtualSchedules struct {
	mutex      sync.Mutex
	schedules  []task.Schedule
	firedCount int
}

func (store *virtualSchedules) ScheduleApprovedCall(_ context.Context, request task.ApprovedCallScheduleRequest) (task.Schedule, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if request.TimeZone == "" {
		request.TimeZone = "Asia/Seoul"
	}
	return task.CreateApprovedCallSchedule(store, request)
}

func (store *virtualSchedules) UpsertSchedule(schedule task.Schedule) error {
	store.schedules = append(store.schedules, schedule)
	return nil
}

func (store *virtualSchedules) ScheduleByID(scheduleID string) (task.Schedule, bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for _, schedule := range store.schedules {
		if schedule.ScheduleID == scheduleID {
			return schedule, true, nil
		}
	}
	return task.Schedule{}, false, nil
}

func (store *virtualSchedules) due() []task.Schedule {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	due := append([]task.Schedule{}, store.schedules[store.firedCount:]...)
	store.firedCount = len(store.schedules)
	sort.Slice(due, func(left int, right int) bool { return due[left].NextRunAt.Before(*due[right].NextRunAt) })
	return due
}

func (harness *VirtualSessionHarness) fireDueApprovedSchedules(ctx context.Context) (VirtualTurnResult, error) {
	turnResult := VirtualTurnResult{Handled: true}
	for _, schedule := range harness.schedules.due() {
		runResult, errorValue := harness.scheduleRunner.RunIfDue(ctx, agentruntime.ScheduleRunRequest{
			Schedule:      schedule,
			ReferenceTime: schedule.NextRunAt.Add(time.Second),
			PersonAccess:  policy.PersonAccess{PersonID: schedule.CreatorPersonID},
			WorkspaceID:   "e2e",
		})
		if errorValue != nil {
			return VirtualTurnResult{}, errorValue
		}
		taskRun := runResult.LaunchResult.TurnResult.TaskRun
		turnResult.TaskRunID = taskRun.TaskRunID
		turnResult.TaskStatus = taskRun.Status
		turnResult.Events = harness.taskEventService.ListTaskEvent(taskRun.TaskRunID)
		turnResult.FinishMessage = runResult.LaunchResult.TurnResult.FinishMessage
		turnResult.DidReply = turnResult.FinishMessage != ""
	}
	return turnResult, nil
}
