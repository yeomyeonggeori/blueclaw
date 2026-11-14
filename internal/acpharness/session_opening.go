package acpharness

import (
	"context"
	"strings"
	"sync/atomic"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const unnamedAgentHarnessName = "acp"

type sessionOpening struct {
	connection        *acp.ClientSideConnection
	agentInitialized  acp.InitializeResponse
	toolCatalogServer acp.McpServer
	sessionMeta       map[string]any
}

type replayGate struct {
	isReplaying atomic.Bool
}

func (harness *Harness) openSession(ctx context.Context, opening sessionOpening, request agentcontract.AgentTurnRequest, replay *replayGate) (acp.SessionId, error) {
	if !canReopenSession(opening.agentInitialized.AgentCapabilities) {
		return harness.newSession(ctx, opening, request)
	}
	harnessName := agentHarnessName(opening.agentInitialized)
	recordedSessionID := harness.recordedSessionID(request, harnessName)
	resumeFailure := ""
	if recordedSessionID != "" {
		resumeError := harness.reopenSession(ctx, opening, request, acp.SessionId(recordedSessionID), replay)
		if resumeError == nil {
			return acp.SessionId(recordedSessionID), nil
		}
		resumeFailure = resumeError.Error()
	}
	sessionID, errorValue := harness.newSession(ctx, opening, request)
	if errorValue != nil {
		return "", errorValue
	}
	harness.recordOpenedSession(request, agentcontract.HarnessSessionOpened{HarnessName: harnessName, SessionID: string(sessionID), ResumeFailure: resumeFailure})
	return sessionID, nil
}

func (harness *Harness) newSession(ctx context.Context, opening sessionOpening, request agentcontract.AgentTurnRequest) (acp.SessionId, error) {
	newSession, errorValue := opening.connection.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        request.WorkingDirectoryPath(),
		McpServers: []acp.McpServer{opening.toolCatalogServer},
		Meta:       opening.sessionMeta,
	})
	return newSession.SessionId, errorValue
}

func canReopenSession(agentCapabilities acp.AgentCapabilities) bool {
	return agentCapabilities.SessionCapabilities.Resume != nil || agentCapabilities.LoadSession
}

func (harness *Harness) reopenSession(ctx context.Context, opening sessionOpening, request agentcontract.AgentTurnRequest, sessionID acp.SessionId, replay *replayGate) error {
	if opening.agentInitialized.AgentCapabilities.SessionCapabilities.Resume != nil {
		_, errorValue := opening.connection.ResumeSession(ctx, acp.ResumeSessionRequest{
			SessionId:  sessionID,
			Cwd:        request.WorkingDirectoryPath(),
			McpServers: []acp.McpServer{opening.toolCatalogServer},
			Meta:       opening.sessionMeta,
		})
		return errorValue
	}
	replay.isReplaying.Store(true)
	defer replay.isReplaying.Store(false)
	_, errorValue := opening.connection.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId:  sessionID,
		Cwd:        request.WorkingDirectoryPath(),
		McpServers: []acp.McpServer{opening.toolCatalogServer},
		Meta:       opening.sessionMeta,
	})
	return errorValue
}

func (harness *Harness) recordedSessionID(request agentcontract.AgentTurnRequest, harnessName string) string {
	if harness.taskRunStore == nil || strings.TrimSpace(request.ExistingTaskRunID) == "" {
		return ""
	}
	return agentcontract.LatestHarnessSessionID(harness.taskRunStore.ListTaskEvent(request.ExistingTaskRunID), harnessName)
}

func (harness *Harness) recordOpenedSession(request agentcontract.AgentTurnRequest, opened agentcontract.HarnessSessionOpened) {
	if harness.taskRunStore == nil || strings.TrimSpace(request.ExistingTaskRunID) == "" {
		return
	}
	harness.taskRunStore.AppendTaskEvent(request.ExistingTaskRunID, agentcontract.TaskEventHarnessSessionOpened, opened.Body())
}

func agentHarnessName(agentInitialized acp.InitializeResponse) string {
	if agentInitialized.AgentInfo == nil || strings.TrimSpace(agentInitialized.AgentInfo.Name) == "" {
		return unnamedAgentHarnessName
	}
	return strings.TrimSpace(agentInitialized.AgentInfo.Name)
}
