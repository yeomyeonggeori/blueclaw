package tui

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/yeomyeonggeori/blueclaw/internal/buildrevision"
)

const (
	mastheadGapWidth   = 4
	mastheadLabelWidth = 11
)

var statusesWorthCounting = []string{
	TaskStatusWaitingApproval,
	TaskStatusWaitingInput,
	TaskStatusRunning,
	TaskStatusBlocked,
	TaskStatusFailed,
}

func clientRevision() string {
	return buildrevision.Short()
}

func renderOverviewField(label string, value string) string {
	return styleMuted.Render(label+strings.Repeat(" ", maximumInt(mastheadLabelWidth-len(label), 1))) + value
}

func renderMasthead(width int, height int, overviewLines []string) string {
	logo := renderLogo(width, height)
	if logo == "" {
		return ""
	}
	overview := strings.Join(overviewLines, "\n")
	if overview == "" || width < logoWidth()+mastheadGapWidth+lipgloss.Width(overview) {
		return logo
	}
	gap := strings.Repeat(" ", mastheadGapWidth)
	return lipgloss.JoinHorizontal(lipgloss.Center, logo, gap, overview)
}

func (model Model) overviewLines() []string {
	lines := []string{
		styleSectionTitle.Render("blueclaw-cli") + "  " + styleMuted.Render(clientRevision()),
		"",
		renderOverviewField("harness", model.harnessDescription()),
		renderOverviewField("tool calls", model.isolationDescription()),
		renderOverviewField("task runs", model.taskRunSummary()),
	}
	return append(lines, renderOverviewField("refreshed", model.refreshDescription()))
}

func (model Model) harnessDescription() string {
	if !model.harnessInfo.IsKnown {
		return styleWarning.Render("unknown")
	}
	return model.harnessInfo.Name
}

func (model Model) isolationDescription() string {
	if !model.harnessInfo.IsKnown {
		return styleMuted.Render("waiting for the daemon")
	}
	if model.harnessInfo.RunsAsRequesterIdentity {
		return styleOK.Render("run as the requester")
	}
	return styleWarning.Render("run as the daemon account")
}

func (model Model) taskRunSummary() string {
	if model.taskRunsError != nil {
		return styleError.Render("unreachable")
	}
	if len(model.taskRuns) == 0 {
		return styleMuted.Render("none yet")
	}
	parts := []string{fmt.Sprintf("%d", len(model.taskRuns))}
	for _, status := range statusesWorthCounting {
		if count := model.countTaskRuns(status); count > 0 {
			parts = append(parts, statusStyle(status).Render(fmt.Sprintf("%d %s", count, status)))
		}
	}
	return strings.Join(parts, styleMuted.Render(" · "))
}

func (model Model) countTaskRuns(status string) int {
	count := 0
	for _, taskRun := range model.taskRuns {
		if taskRun.Status == status {
			count++
		}
	}
	return count
}

func (model Model) refreshDescription() string {
	return model.now.Format("15:04:05") + styleMuted.Render(fmt.Sprintf(" · every %s", model.pollInterval))
}

func (setupModel SetupModel) overviewLines() []string {
	return []string{
		styleSectionTitle.Render("blueclaw-cli") + "  " + styleMuted.Render(clientRevision()),
		"",
		renderOverviewField("state", styleWarning.Render("not enrolled yet")),
		renderOverviewField("writes to", setupModel.home.DirectoryPath),
		renderOverviewField("harness", setupModel.selectedHarnessName()),
	}
}

func (setupModel SetupModel) selectedHarnessName() string {
	if setupModel.harnessIndex < 0 || setupModel.harnessIndex >= len(setupModel.availableHarness) {
		return styleMuted.Render("none available")
	}
	return setupModel.availableHarness[setupModel.harnessIndex].Name
}
