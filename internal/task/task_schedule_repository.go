package task

import "time"

type TaskScheduleCancelScope string

const (
	TaskScheduleCancelScopeCurrentConversation TaskScheduleCancelScope = "currentConversation"
	TaskScheduleCancelScopeMine                TaskScheduleCancelScope = "mine"
	TaskScheduleCancelScopeScheduleIDs         TaskScheduleCancelScope = "scheduleIDs"
)

type TaskScheduleCancelRequest struct {
	Scope             TaskScheduleCancelScope
	RequesterPersonID string
	ConversationID    string
	TaskScheduleIDs   []string
	CancelledAt       time.Time
}

type TaskScheduleCancelResult struct {
	TaskSchedules []TaskSchedule `json:"taskSchedules"`
}

type TaskScheduleUpdateRequest struct {
	TaskScheduleID     string
	RequesterPersonID  string
	UpdateTaskSchedule func(TaskSchedule) (TaskSchedule, error)
}

type TaskScheduleUpdateResult struct {
	TaskSchedule TaskSchedule `json:"taskSchedule"`
	IsFound      bool         `json:"isFound"`
}

type TaskScheduleDeleteRequest struct {
	TaskScheduleID    string
	RequesterPersonID string
}

type TaskScheduleDeleteResult struct {
	TaskSchedule TaskSchedule `json:"taskSchedule"`
	IsFound      bool         `json:"isFound"`
}

type TaskScheduleSummary struct {
	ActiveCount       int        `json:"activeCount"`
	UnboundedCount    int        `json:"unboundedCount"`
	IntervalCount     int        `json:"intervalCount"`
	CronCount         int        `json:"cronCount"`
	OnceCount         int        `json:"onceCount"`
	EarliestNextRunAt *time.Time `json:"earliestNextRunAt,omitempty"`
	LatestNextRunAt   *time.Time `json:"latestNextRunAt,omitempty"`
	CheckedAt         time.Time  `json:"checkedAt"`
}

type TaskScheduleListRequest struct {
	ConversationID  string
	CreatorPersonID string
	UnboundedOnly   bool
	IncludeExpired  bool
	Page            int
	PageSize        int
	ReferenceTime   time.Time
}

type TaskScheduleListResult struct {
	TaskSchedules []TaskSchedule
	TotalCount    int
	Page          int
	PageSize      int
}

type TaskScheduleCreatorRepairRequest struct {
	FromCreatorPersonID string
	ToCreatorPersonID   string
}

type TaskScheduleCreatorRepairResult struct {
	UpdatedCount int
}

type TaskScheduleTimeZoneRepairRepository interface {
	CountEmptyTaskScheduleTimeZone() (int, error)
	FillEmptyTaskScheduleTimeZone(string) (int, error)
}

type TaskScheduleRepository interface {
	UpsertTaskSchedule(TaskSchedule) error
	UpdateTaskSchedule(TaskScheduleUpdateRequest) (TaskScheduleUpdateResult, error)
	ClaimDueTaskSchedules(int, time.Duration, time.Time, string) ([]TaskSchedule, error)
	MarkTaskScheduleSucceeded(TaskSchedule) error
	MarkTaskScheduleFailed(TaskSchedule, string, time.Time) error
	ExpireTaskSchedule(TaskSchedule, string, time.Time) error
	CancelTaskSchedules(TaskScheduleCancelRequest) (TaskScheduleCancelResult, error)
	ListTaskSchedules(TaskScheduleListRequest) (TaskScheduleListResult, error)
}

type TaskWaitTokenRepository interface {
	InsertTaskWaitToken(TaskWaitToken) error
	FindOpenByWaitID(string) (TaskWaitToken, bool, error)
	FindOpenByPersonConversationAndReplyTarget(string, string, string, string) (TaskWaitToken, bool, error)
	FindOpenByPersonConversationAndThreadRoot(string, string, string, string) (TaskWaitToken, bool, error)
	FindOpenByPersonConversationAndDispatchID(string, string, string, string) (TaskWaitToken, bool, error)
	FindOpenByPersonTaskRunAndInteraction(string, string, string) (TaskWaitToken, bool, error)
	FindOpenByPersonAndConversation(string, string, string) ([]TaskWaitToken, error)
	ResolveTaskWait(string, time.Time) error
	ExpireOldTaskWaits(time.Time) ([]string, error)
	ExpireTaskWaitTokensForPerson(string, time.Time) ([]string, error)
}
