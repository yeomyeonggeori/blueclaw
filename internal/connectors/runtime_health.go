package connectors

import (
	"context"
	"time"
)

type ConnectorRuntimeHealth struct {
	Started               bool      `json:"started"`
	HasEventRepository    bool      `json:"hasEventRepository"`
	HasQueueRepository    bool      `json:"hasQueueRepository"`
	HasOutboxRepository   bool      `json:"hasOutboxRepository"`
	RegisteredPlatforms   []string  `json:"registeredPlatforms"`
	HasPlatformAdapter    bool      `json:"hasPlatformAdapter"`
	InboxWorkerCount      int       `json:"inboxWorkerCount"`
	OutboxWorkerCount     int       `json:"outboxWorkerCount"`
	InboxWorkersAlive     bool      `json:"inboxWorkersAlive"`
	OutboxWorkersAlive    bool      `json:"outboxWorkersAlive"`
	LastInboxHeartbeatAt  time.Time `json:"lastInboxHeartbeatAt,omitempty"`
	LastOutboxHeartbeatAt time.Time `json:"lastOutboxHeartbeatAt,omitempty"`
	Passed                bool      `json:"passed"`
}

func (connectorRuntime *ConnectorRuntime) Health() ConnectorRuntimeHealth {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	platforms := []string{}
	for platform := range connectorRuntime.adapterByPlatform {
		platforms = append(platforms, platform)
	}
	health := ConnectorRuntimeHealth{
		Started:               connectorRuntime.started,
		HasEventRepository:    connectorRuntime.eventRepository != nil,
		HasQueueRepository:    connectorRuntime.queueRepository() != nil,
		HasOutboxRepository:   connectorRuntime.outboxRepository() != nil,
		RegisteredPlatforms:   platforms,
		HasPlatformAdapter:    len(platforms) > 0,
		InboxWorkerCount:      len(connectorRuntime.inboxHeartbeats),
		OutboxWorkerCount:     len(connectorRuntime.outboxHeartbeats),
		LastInboxHeartbeatAt:  latestTime(connectorRuntime.inboxHeartbeats),
		LastOutboxHeartbeatAt: latestTime(connectorRuntime.outboxHeartbeats),
	}
	health.InboxWorkersAlive = connectorWorkersAlive(connectorRuntime.inboxHeartbeats, 2*connectorWorkerIdleDelay)
	health.OutboxWorkersAlive = connectorWorkersAlive(connectorRuntime.outboxHeartbeats, 2*connectorWorkerIdleDelay)
	health.Passed = health.Started &&
		health.HasEventRepository &&
		health.HasQueueRepository &&
		health.HasOutboxRepository &&
		health.HasPlatformAdapter &&
		health.InboxWorkersAlive &&
		health.OutboxWorkersAlive
	return health
}

func (connectorRuntime *ConnectorRuntime) prepareConnectorWorkers(kind string, count int) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	heartbeats := make([]time.Time, count)
	now := time.Now()
	for index := range heartbeats {
		heartbeats[index] = now
	}
	if kind == "inbox" {
		connectorRuntime.inboxHeartbeats = heartbeats
		return
	}
	connectorRuntime.outboxHeartbeats = heartbeats
}

func (connectorRuntime *ConnectorRuntime) recordConnectorWorkerHeartbeat(kind string, workerIndex int) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	now := time.Now()
	if kind == "inbox" && workerIndex >= 0 && workerIndex < len(connectorRuntime.inboxHeartbeats) {
		connectorRuntime.inboxHeartbeats[workerIndex] = now
		return
	}
	if kind == "outbox" && workerIndex >= 0 && workerIndex < len(connectorRuntime.outboxHeartbeats) {
		connectorRuntime.outboxHeartbeats[workerIndex] = now
	}
}

func (connectorRuntime *ConnectorRuntime) recordConnectorWorkerHeartbeatUntilStopped(ctx context.Context, kind string, workerIndex int) {
	ticker := time.NewTicker(connectorWorkerIdleDelay)
	defer ticker.Stop()
	for {
		connectorRuntime.recordConnectorWorkerHeartbeat(kind, workerIndex)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func connectorWorkersAlive(heartbeats []time.Time, maximumAge time.Duration) bool {
	if len(heartbeats) == 0 {
		return false
	}
	oldestAllowed := time.Now().Add(-maximumAge)
	for _, heartbeat := range heartbeats {
		if heartbeat.IsZero() || heartbeat.Before(oldestAllowed) {
			return false
		}
	}
	return true
}

func latestTime(values []time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}
