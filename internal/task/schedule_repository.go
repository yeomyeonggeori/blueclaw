package task

import "time"

type ScheduleCancelScope string

const (
	ScheduleCancelScopeCurrentConversation ScheduleCancelScope = "currentConversation"
	ScheduleCancelScopeMine                ScheduleCancelScope = "mine"
	ScheduleCancelScopeScheduleIDs         ScheduleCancelScope = "scheduleIDs"
)

type ScheduleCancelRequest struct {
	Scope             ScheduleCancelScope
	RequesterPersonID string
	ConversationID    string
	ScheduleIDs       []string
	CancelledAt       time.Time
}

type ScheduleCancelResult struct {
	Schedules []Schedule `json:"taskSchedules"`
}

type ScheduleUpdateRequest struct {
	ScheduleID        string
	RequesterPersonID string
	UpdateSchedule    func(Schedule) (Schedule, error)
}

type ScheduleUpdateResult struct {
	Schedule Schedule `json:"taskSchedule"`
	IsFound  bool     `json:"isFound"`
}

type ScheduleDeleteRequest struct {
	ScheduleID        string
	RequesterPersonID string
}

type ScheduleDeleteResult struct {
	Schedule Schedule `json:"taskSchedule"`
	IsFound  bool     `json:"isFound"`
}

type ScheduleSummary struct {
	ActiveCount       int        `json:"activeCount"`
	UnboundedCount    int        `json:"unboundedCount"`
	IntervalCount     int        `json:"intervalCount"`
	CronCount         int        `json:"cronCount"`
	OnceCount         int        `json:"onceCount"`
	EarliestNextRunAt *time.Time `json:"earliestNextRunAt,omitempty"`
	LatestNextRunAt   *time.Time `json:"latestNextRunAt,omitempty"`
	CheckedAt         time.Time  `json:"checkedAt"`
}

type ScheduleListRequest struct {
	ConversationID  string
	CreatorPersonID string
	UnboundedOnly   bool
	IncludeExpired  bool
	Page            int
	PageSize        int
	ReferenceTime   time.Time
}

type ScheduleListResult struct {
	Schedules  []Schedule
	TotalCount int
	Page       int
	PageSize   int
}

type ScheduleCreatorRepairRequest struct {
	FromCreatorPersonID string
	ToCreatorPersonID   string
}

type ScheduleCreatorRepairResult struct {
	UpdatedCount int
}

type ScheduleTimeZoneRepairRepository interface {
	CountEmptyScheduleTimeZone() (int, error)
	FillEmptyScheduleTimeZone(string) (int, error)
}

type ScheduleRepository interface {
	UpsertSchedule(Schedule) error
	UpdateSchedule(ScheduleUpdateRequest) (ScheduleUpdateResult, error)
	ClaimDueSchedules(int, time.Duration, time.Time, string) ([]Schedule, error)
	MarkScheduleSucceeded(Schedule) error
	MarkScheduleFailed(Schedule, string, time.Time) error
	ExpireSchedule(Schedule, string, time.Time) error
	CancelSchedules(ScheduleCancelRequest) (ScheduleCancelResult, error)
	ListSchedules(ScheduleListRequest) (ScheduleListResult, error)
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
