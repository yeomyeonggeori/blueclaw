package connectors

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const deliveryNoticePhase = "delivery"

type FilesNotDelivered struct {
	Delivered   []toolcontract.FileAttachment
	Undelivered []toolcontract.FileAttachment
	Reason      string
}

func (notDelivered *FilesNotDelivered) Error() string {
	return strings.Join(agentcontract.FailureReportAttachmentFilenames(notDelivered.Undelivered), ", ") + " did not reach the person: " + notDelivered.Reason
}

func filesNotDeliveredIn(errorValue error) (*FilesNotDelivered, bool) {
	notDelivered := &FilesNotDelivered{}
	isNotDelivered := errors.As(errorValue, &notDelivered)
	return notDelivered, isNotDelivered
}

func (connectorRuntime *ConnectorRuntime) tellOfFilesNotDelivered(ctx context.Context, turn *inboundTurn, taskRunID string, notDelivered *FilesNotDelivered) {
	report := deliveryFailureReport(turn.event, taskRunID, notDelivered)
	notice, generation := (agentcontract.FailureNoticeGenerator{LanguageModel: connectorRuntime.noticeLanguageModel}).Generate(ctx, report)
	connectorRuntime.appendTaskEvent(taskRunID, task.TaskEventConnectorFilesUndelivered, map[string]any{
		"phase":      deliveryNoticePhase,
		"report":     report,
		"generation": generation,
	})
	reply := OutboundReply{
		Message:       notice.SendableMessage(),
		TaskRunID:     taskRunID,
		ReplyKind:     connectorReplyKindDeliveryFailureNotice,
		FailureNotice: notice,
	}
	dispatchID, errorValue := turn.sendReply(withConnectorEvent(ctx, turn.event), turn.replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplyFailed, connectorReplyEventBody(turn.event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+turn.platform+".delivery_notice.failed", slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".delivery_notice.sent", slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID))
}

func deliveryFailureReport(event PlatformInboundEvent, taskRunID string, notDelivered *FilesNotDelivered) agentcontract.FailureReport {
	undelivered := agentcontract.FailureReportAttachmentFilenames(notDelivered.Undelivered)
	delivered := agentcontract.FailureReportAttachmentFilenames(notDelivered.Delivered)
	return agentcontract.FailureReport{
		Phase:               deliveryNoticePhase,
		StopReason:          "file_not_delivered",
		FailedOperation:     "file delivery",
		SafeFailureSummary:  "These requested files did not reach the person: " + strings.Join(undelivered, ", ") + ".",
		RawError:            notDelivered.Error(),
		OriginalRequest:     event.Prompt,
		ResponseLanguage:    firstNonEmptyString(event.ResponseLanguage, event.Context.ResponseLanguage),
		ArtifactRequired:    true,
		HasAttachments:      len(delivered) > 0,
		AttachmentFilenames: delivered,
		DiagnosticEventID:   strings.TrimSpace(taskRunID) + ":" + deliveryNoticePhase,
	}
}

func (connectorRuntime *ConnectorRuntime) appendTaskEvent(taskRunID string, name string, body any) {
	if connectorRuntime.taskRunService == nil || strings.TrimSpace(taskRunID) == "" {
		return
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, name, agentruntime.MarshalBody(body))
}
