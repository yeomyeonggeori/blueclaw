package tui

// FilterWaitingApproval returns only the task runs currently paused for
// approval. This is the entire population the Approvals screen shows.
func FilterWaitingApproval(taskRuns []TaskRun) []TaskRun {
	waitingTaskRuns := make([]TaskRun, 0, len(taskRuns))
	for _, taskRun := range taskRuns {
		if taskRun.Status == TaskStatusWaitingApproval {
			waitingTaskRuns = append(waitingTaskRuns, taskRun)
		}
	}
	return waitingTaskRuns
}

// ApprovalDecisionForKey maps the Approvals screen's single-key shortcuts to
// the wire decision value the admin API expects.
func ApprovalDecisionForKey(key string) (string, bool) {
	switch key {
	case "y":
		return ApprovalDecisionApprove, true
	case "n":
		return ApprovalDecisionReject, true
	default:
		return "", false
	}
}
