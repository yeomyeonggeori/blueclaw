package connectors

import (
	"context"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type PendingRequestMessage struct {
	SourceReference  string                    `json:"sourceReference"`
	MessageID        string                    `json:"messageID,omitempty"`
	Prompt           string                    `json:"prompt"`
	InputParts       []agentcontract.AgentPart `json:"inputParts,omitempty"`
	InputAttachments []InputAttachment         `json:"inputAttachments,omitempty"`
}

type pendingRequest struct {
	event        PlatformInboundEvent
	previous     *pendingRequest
	cancel       context.CancelFunc
	done         chan struct{}
	isRunning    bool
	isFinished   bool
	isSuperseded bool
	hasReply     bool
}

type pendingRequestStore struct {
	mutex                sync.Mutex
	requests             map[string]*pendingRequest
	supersededReferences map[string]bool
}

type pendingRequestStartReason string

const (
	pendingRequestStarted        pendingRequestStartReason = "started"
	pendingRequestSuperseded     pendingRequestStartReason = "superseded"
	pendingRequestAlreadyRunning pendingRequestStartReason = "already_running"
)

func newPendingRequestStore() *pendingRequestStore {
	return &pendingRequestStore{requests: map[string]*pendingRequest{}, supersededReferences: map[string]bool{}}
}

func pendingRequestsShareScope(previous PlatformInboundEvent, current PlatformInboundEvent) bool {
	if previous.Platform != current.Platform || previous.SenderID != current.SenderID || previous.ConversationID != current.ConversationID {
		return false
	}
	if eventIsThreadReply(current) {
		return previous.ReplyTargetID == current.ReplyTargetID
	}
	return !eventIsThreadReply(previous)
}

func (store *pendingRequestStore) prepare(event PlatformInboundEvent) (PlatformInboundEvent, *pendingRequest) {
	if exactTaskControlIntent(event.Prompt) != agentcontract.TaskControlIntentNone {
		return event, nil
	}
	if request := store.requests[event.DedupeKey()]; request != nil {
		return request.event, nil
	}
	for _, request := range store.requests {
		if request.hasReply || request.isSuperseded || !pendingRequestsShareScope(request.event, event) {
			continue
		}
		if len(event.PreviousMessages) == 0 {
			event.PreviousMessages = append(append([]PendingRequestMessage{}, request.event.PreviousMessages...), pendingMessageFromEvent(request.event))
		}
		return event, request
	}
	return event, nil
}

func pendingMessageFromEvent(event PlatformInboundEvent) PendingRequestMessage {
	return PendingRequestMessage{
		SourceReference:  event.DedupeKey(),
		MessageID:        event.MessageID,
		Prompt:           event.Prompt,
		InputParts:       event.InputParts,
		InputAttachments: event.Context.InputAttachments,
	}
}

func (store *pendingRequestStore) register(event PlatformInboundEvent, previous *pendingRequest) {
	if exactTaskControlIntent(event.Prompt) != agentcontract.TaskControlIntentNone {
		return
	}
	if store.requests[event.DedupeKey()] != nil {
		return
	}
	store.requests[event.DedupeKey()] = &pendingRequest{event: event, previous: previous, done: make(chan struct{})}
	for _, message := range event.PreviousMessages {
		store.supersededReferences[message.SourceReference] = true
	}
	if previous == nil {
		return
	}
	previous.isSuperseded = true
	if previous.cancel != nil {
		previous.cancel()
	}
	if !previous.isRunning && !previous.isFinished {
		previous.isFinished = true
		close(previous.done)
	}
	if previous.isFinished {
		delete(store.requests, previous.event.DedupeKey())
		store.requests[event.DedupeKey()].previous = previous.previous
	}
}

func (store *pendingRequestStore) begin(ctx context.Context, event PlatformInboundEvent) (context.Context, *pendingRequest, pendingRequestStartReason) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.supersededReferences[event.DedupeKey()] {
		return ctx, nil, pendingRequestSuperseded
	}
	request := store.requests[event.DedupeKey()]
	if request == nil {
		event, previous := store.prepare(event)
		store.register(event, previous)
		request = store.requests[event.DedupeKey()]
	}
	if request.isSuperseded {
		return ctx, request, pendingRequestSuperseded
	}
	if request.isRunning {
		return ctx, request, pendingRequestAlreadyRunning
	}
	if request.isFinished {
		request.done = make(chan struct{})
		request.isFinished = false
	}
	request.isRunning = true
	requestContext, cancel := context.WithCancel(ctx)
	request.cancel = cancel
	return requestContext, request, pendingRequestStarted
}

func (store *pendingRequestStore) finish(request *pendingRequest) bool {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	request.isRunning = false
	if !request.isFinished {
		request.isFinished = true
		close(request.done)
	}
	if request.isSuperseded || request.hasReply {
		delete(store.requests, request.event.DedupeKey())
	}
	if request.cancel != nil {
		request.cancel()
	}
	return request.isSuperseded
}

func (store *pendingRequestStore) isSuperseded(sourceReference string) bool {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return store.supersededReferences[sourceReference]
}

func revisedRequestEvent(event PlatformInboundEvent) PlatformInboundEvent {
	if len(event.PreviousMessages) == 0 {
		return event
	}
	prompts := []string{"The following user messages arrived in order before a reply. They form one revised request. Apply later corrections to earlier details and handle the final combined request once."}
	inputParts := []agentcontract.AgentPart{}
	inputAttachments := []InputAttachment{}
	for _, message := range currentRequestMessages(event) {
		prompts = append(prompts, message.Prompt)
		inputParts = append(inputParts, message.InputParts...)
		inputAttachments = append(inputAttachments, message.InputAttachments...)
	}
	event.Prompt = strings.Join(prompts, "\n\n")
	event.InputParts = inputParts
	event.Context.InputAttachments = inputAttachments
	return event
}

func currentRequestMessages(event PlatformInboundEvent) []PendingRequestMessage {
	messages := append(append([]PendingRequestMessage{}, event.PreviousMessages...), pendingMessageFromEvent(event))
	latestIndexes := map[string]int{}
	for index, message := range messages {
		latestIndexes[pendingMessageIdentity(event, message)] = index
	}
	currentMessages := []PendingRequestMessage{}
	for index, message := range messages {
		if latestIndexes[pendingMessageIdentity(event, message)] == index {
			currentMessages = append(currentMessages, message)
		}
	}
	return currentMessages
}

func pendingMessageIdentity(event PlatformInboundEvent, message PendingRequestMessage) string {
	if message.MessageID == "" {
		return message.SourceReference
	}
	return event.Platform + ":" + event.ConversationID + ":" + message.MessageID
}
