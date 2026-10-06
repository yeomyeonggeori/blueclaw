package acpsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const (
	DeliveryMetaKey            = "kim.intern/delivery"
	DeliveredExtensionMethod   = "_kim.intern/delivered"
	UndeliveredExtensionMethod = "_kim.intern/undelivered"
)

type Delivery struct {
	DeliveryID      string `json:"deliveryID,omitempty"`
	ReplyTargetID   string `json:"replyTargetID,omitempty"`
	IsAlreadyPosted bool   `json:"isAlreadyPosted,omitempty"`
	IsFinal         bool   `json:"isFinal,omitempty"`
}

type DeliveredReport struct {
	DeliveryID string `json:"deliveryID"`
	MessageID  string `json:"messageID"`
}

type UndeliveredReport struct {
	DeliveryID string `json:"deliveryID"`
	Reason     string `json:"reason"`
}

const defaultDeliveryReportWait = time.Minute

var (
	errNobodyAwaitsThatDelivery = errors.New("nothing is waiting to hear about a delivery by that id")
	errReportNamesNoDelivery    = errors.New("a delivery report names no delivery")
	errRelayLeftBeforeReporting = errors.New("the relay went away before it said whether the person was reached")
	errAnsweredWithoutReporting = errors.New("the relay answered the question without saying whether it reached the person")
	errFileHasNoPath            = errors.New("the file names no path on this machine, and the relay posts a file only by its path")
)

type deliveryOutcome struct {
	messageID string
	failure   error
}

type awaitedDeliveries struct {
	mutex   sync.Mutex
	waiting map[string]chan deliveryOutcome
}

func newAwaitedDeliveries() *awaitedDeliveries {
	return &awaitedDeliveries{waiting: map[string]chan deliveryOutcome{}}
}

func (deliveries *awaitedDeliveries) expect() (string, <-chan deliveryOutcome) {
	deliveryID := newRandomIdentifier()
	return deliveryID, deliveries.expectDelivery(deliveryID)
}

func (deliveries *awaitedDeliveries) expectDelivery(deliveryID string) <-chan deliveryOutcome {
	outcome := make(chan deliveryOutcome, 1)
	deliveries.mutex.Lock()
	defer deliveries.mutex.Unlock()
	deliveries.waiting[deliveryID] = outcome
	return outcome
}

func (deliveries *awaitedDeliveries) forget(deliveryID string) {
	deliveries.mutex.Lock()
	defer deliveries.mutex.Unlock()
	delete(deliveries.waiting, deliveryID)
}

func (deliveries *awaitedDeliveries) settle(deliveryID string, outcome deliveryOutcome) error {
	if strings.TrimSpace(deliveryID) == "" {
		return errReportNamesNoDelivery
	}
	deliveries.mutex.Lock()
	defer deliveries.mutex.Unlock()
	waiting, isWaiting := deliveries.waiting[deliveryID]
	if !isWaiting {
		return errNobodyAwaitsThatDelivery
	}
	delete(deliveries.waiting, deliveryID)
	waiting <- outcome
	return nil
}

func (agent *Agent) settleDelivered(params json.RawMessage) (any, error) {
	report := DeliveredReport{}
	if errorValue := json.Unmarshal(params, &report); errorValue != nil {
		return nil, errorValue
	}
	return map[string]any{}, agent.deliveries.settle(report.DeliveryID, deliveryOutcome{messageID: strings.TrimSpace(report.MessageID)})
}

func (agent *Agent) settleUndelivered(params json.RawMessage) (any, error) {
	report := UndeliveredReport{}
	if errorValue := json.Unmarshal(params, &report); errorValue != nil {
		return nil, errorValue
	}
	failure := fmt.Errorf("the relay could not reach the person: %s", strings.TrimSpace(report.Reason))
	return map[string]any{}, agent.deliveries.settle(report.DeliveryID, deliveryOutcome{failure: failure})
}

func (agent *Agent) deliverReply(ctx context.Context, sessionID acp.SessionId, delivery Delivery, reply connectors.OutboundReply) (string, error) {
	fileMessageID, errorValue := agent.deliverFiles(ctx, sessionID, delivery, reply.Attachments)
	if errorValue != nil {
		return "", errorValue
	}
	message := strings.TrimSpace(reply.Message)
	if message == "" {
		return fileMessageID, nil
	}
	return agent.deliver(ctx, sessionID, delivery, acp.UpdateAgentMessageText(message))
}

func (agent *Agent) deliverFiles(ctx context.Context, sessionID acp.SessionId, delivery Delivery, attachments []toolcontract.FileAttachment) (string, error) {
	firstMessageID := ""
	notDelivered := &connectors.FilesNotDelivered{}
	for _, attachment := range attachments {
		messageID, errorValue := agent.deliverFile(ctx, sessionID, delivery, attachment)
		if errorValue != nil {
			notDelivered.Undelivered = append(notDelivered.Undelivered, attachment)
			notDelivered.Reason = joinedReasons(notDelivered.Reason, errorValue.Error())
			continue
		}
		notDelivered.Delivered = append(notDelivered.Delivered, attachment)
		firstMessageID = firstNonEmpty(firstMessageID, messageID)
	}
	if len(notDelivered.Undelivered) > 0 {
		return "", notDelivered
	}
	return firstMessageID, nil
}

func (agent *Agent) deliverFile(ctx context.Context, sessionID acp.SessionId, delivery Delivery, attachment toolcontract.FileAttachment) (string, error) {
	devicePath := strings.TrimSpace(attachment.DevicePath)
	if devicePath == "" {
		return "", errFileHasNoPath
	}
	return agent.deliver(ctx, sessionID, delivery, acp.UpdateAgentMessage(resourceLinkOf(attachment, devicePath)))
}

func joinedReasons(reasons string, reason string) string {
	if reasons == "" || strings.Contains(reasons, reason) {
		return firstNonEmpty(reasons, reason)
	}
	return reasons + "; " + reason
}

func (agent *Agent) deliver(ctx context.Context, sessionID acp.SessionId, delivery Delivery, update acp.SessionUpdate) (string, error) {
	outcome := agent.deliveries.expectDelivery(delivery.DeliveryID)
	defer agent.deliveries.forget(delivery.DeliveryID)
	if errorValue := agent.notifyDelivery(ctx, sessionID, delivery, update); errorValue != nil {
		return "", errorValue
	}
	return agent.awaitDelivery(ctx, outcome, nil)
}

func (agent *Agent) notifyDelivery(ctx context.Context, sessionID acp.SessionId, delivery Delivery, update acp.SessionUpdate) error {
	return agent.connection.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: sessionID,
		Update:    update,
		Meta:      deliveryMeta(delivery),
	})
}

func (agent *Agent) awaitDelivery(ctx context.Context, outcome <-chan deliveryOutcome, askingEnded <-chan struct{}) (string, error) {
	timer := time.NewTimer(agent.deliveryReportWait)
	defer timer.Stop()
	select {
	case settled := <-outcome:
		return settled.messageID, settled.failure
	case <-askingEnded:
		return settledOrUnreported(outcome)
	case <-agent.connection.Done():
		return "", errRelayLeftBeforeReporting
	case <-ctx.Done():
		return "", fmt.Errorf("stopped waiting to hear whether the relay reached the person: %w", ctx.Err())
	case <-timer.C:
		return "", fmt.Errorf("the relay did not say within %s whether it reached the person", agent.deliveryReportWait)
	}
}

func settledOrUnreported(outcome <-chan deliveryOutcome) (string, error) {
	select {
	case settled := <-outcome:
		return settled.messageID, settled.failure
	default:
		return "", errAnsweredWithoutReporting
	}
}

func deliveryMeta(delivery Delivery) map[string]any {
	return map[string]any{DeliveryMetaKey: delivery}
}

type permissionAsking struct {
	response   acp.RequestPermissionResponse
	errorValue error
}

func (agent *Agent) askThePerson(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question string, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	deliveryID, outcome := agent.deliveries.expect()
	defer agent.deliveries.forget(deliveryID)
	request.Meta = deliveryMeta(Delivery{DeliveryID: deliveryID, ReplyTargetID: approvalRequest.ReplyTargetID})
	asked := make(chan permissionAsking, 1)
	askingEnded := make(chan struct{})
	go func() {
		response, errorValue := agent.connection.RequestPermission(ctx, request)
		asked <- permissionAsking{response: response, errorValue: errorValue}
		close(askingEnded)
	}()
	sessionTurn := agent.sessionTurns.OpenSessionTurn(ctx, questionEventOf(approvalRequest), approvalRequest.RequesterPersonID, func(ctx context.Context, _ connectors.ReplyTarget, _ connectors.OutboundReply) (string, error) {
		return agent.awaitDelivery(ctx, outcome, askingEnded)
	})
	sessionTurn.DeliverApprovalQuestion(ctx, approvalRequest.TaskRunID, question)
	answered := <-asked
	return answered.response, answered.errorValue
}

func questionEventOf(approvalRequest mcpserver.ApprovalRequest) connectors.PlatformInboundEvent {
	return connectors.PlatformInboundEvent{
		Platform:       approvalRequest.Platform,
		ConversationID: approvalRequest.ConversationID,
		ReplyTargetID:  approvalRequest.ReplyTargetID,
	}
}
