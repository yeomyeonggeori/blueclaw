package httpserver

import (
	"net/http"
	"net/http/pprof"
	"os"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/userapi"
)

type RouterDependencies struct {
	PolicyHandler         adminapi.PolicyHandler
	IdentityResolve       adminapi.IdentityResolveHandler
	HealthHandler         HealthHandler
	AuditHandler          *adminapi.AuditHandler
	AttentionHandler      adminapi.AttentionHandler
	TaskMonitorHandler    adminapi.TaskMonitorHandler
	TaskRunHandler        adminapi.TaskRunHandler
	TaskApprovalHandler   adminapi.TaskApprovalHandler
	HarnessStatusHandler  adminapi.HarnessStatusHandler
	SkillInventoryHandler adminapi.SkillInventoryHandler
	TaskSearchHandler     adminapi.TaskSearchHandler
	QuiesceHandler        adminapi.QuiesceHandler
	TaskScheduleHandler   adminapi.TaskScheduleHandler
	ConnectorDiagnostics  adminapi.ConnectorEventDiagnosticHandler
	ConversationReset     adminapi.ConversationResetHandler
	BackupHandler         adminapi.BackupHandler
	TaskInboxHandler      userapi.TaskInboxHandler
	TaskActionHandler     userapi.TaskActionHandler
	SSEHandler            SSEHandler
	ConnectorEventHandler *ConnectorEventHandler
	AgentReplyHandler     AgentReplyHandler
	WorkspaceFilesHandler WorkspaceFilesHandler
	ToolCatalogHandler    http.Handler
}

func NewRouter(routerDependencies RouterDependencies) http.Handler {
	multiplexer := http.NewServeMux()

	if routerDependencies.ToolCatalogHandler != nil {
		multiplexer.Handle("/harness/tool-catalog", routerDependencies.ToolCatalogHandler)
		multiplexer.Handle("/harness/tool-catalog/", routerDependencies.ToolCatalogHandler)
	}
	multiplexer.HandleFunc("GET /admin/api/health", routerDependencies.HealthHandler.HandleHealth)
	multiplexer.HandleFunc("GET /debug/pprof/", pprof.Index)
	multiplexer.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	multiplexer.HandleFunc("GET /admin/api/policy", routerDependencies.PolicyHandler.HandleGetPolicy)
	multiplexer.HandleFunc("POST /admin/api/policy/validate", routerDependencies.PolicyHandler.HandleValidatePolicy)
	multiplexer.HandleFunc("POST /admin/api/policy/save", routerDependencies.PolicyHandler.HandleSavePolicy)
	multiplexer.HandleFunc("POST /admin/api/policy/reload", routerDependencies.PolicyHandler.HandleReloadPolicy)
	multiplexer.HandleFunc("POST /admin/api/people/invite", routerDependencies.PolicyHandler.HandleInvitePerson)
	multiplexer.HandleFunc("DELETE /admin/api/people", routerDependencies.PolicyHandler.HandleRemovePerson)
	multiplexer.HandleFunc("POST /admin/api/people/canonicalize-references", routerDependencies.PolicyHandler.HandleCanonicalizePersonReferences)
	multiplexer.HandleFunc("POST /admin/api/identity/resolve-recipient", routerDependencies.IdentityResolve.HandleResolveRecipient)
	multiplexer.HandleFunc("GET /admin/api/audit", routerDependencies.AuditHandler.HandleListAudit)
	multiplexer.HandleFunc("POST /admin/api/attention/run", routerDependencies.AttentionHandler.HandleRunAttention)
	multiplexer.HandleFunc("GET /admin/api/run", routerDependencies.TaskMonitorHandler.HandleListTaskRun)
	multiplexer.HandleFunc("GET /admin/api/run/search", routerDependencies.TaskSearchHandler.HandleSearchTaskRuns)
	multiplexer.HandleFunc("GET /admin/api/run/detail", routerDependencies.TaskMonitorHandler.HandleGetTaskRun)
	multiplexer.HandleFunc("POST /admin/api/run/delete", routerDependencies.TaskMonitorHandler.HandleDeleteTaskRun)
	multiplexer.HandleFunc("POST /admin/api/run/start", routerDependencies.TaskRunHandler.HandleRunTask)
	multiplexer.HandleFunc("POST /admin/api/run/cancel", routerDependencies.TaskRunHandler.HandleCancelTaskRun)
	multiplexer.HandleFunc("POST /admin/api/run/approve", routerDependencies.TaskApprovalHandler.HandleApproveTaskRun)
	multiplexer.HandleFunc("GET /admin/api/harness", routerDependencies.HarnessStatusHandler.HandleGetHarnessStatus)
	multiplexer.HandleFunc("GET /admin/api/skills", routerDependencies.SkillInventoryHandler.HandleListSkills)
	multiplexer.HandleFunc("GET /admin/api/quiesce", routerDependencies.QuiesceHandler.HandleGet)
	multiplexer.HandleFunc("POST /admin/api/quiesce", routerDependencies.QuiesceHandler.HandlePost)
	multiplexer.HandleFunc("POST /admin/api/runtime/prepare-shutdown", routerDependencies.QuiesceHandler.HandlePrepareShutdown)
	multiplexer.HandleFunc("GET /admin/api/schedule", routerDependencies.TaskScheduleHandler.HandleList)
	multiplexer.HandleFunc("POST /admin/api/schedule/cancel", routerDependencies.TaskScheduleHandler.HandleCancel)
	multiplexer.HandleFunc("POST /admin/api/schedule/delete", routerDependencies.TaskScheduleHandler.HandleDelete)
	multiplexer.HandleFunc("POST /admin/api/schedule/update", routerDependencies.TaskScheduleHandler.HandleUpdate)
	multiplexer.HandleFunc("POST /admin/api/schedule/repair-creator", routerDependencies.TaskScheduleHandler.HandleRepairCreator)
	multiplexer.HandleFunc("GET /admin/api/schedule/summary", routerDependencies.TaskScheduleHandler.HandleSummary)
	multiplexer.HandleFunc("GET /admin/api/connector/events", routerDependencies.ConnectorDiagnostics.HandleList)
	multiplexer.HandleFunc("POST /admin/api/conversation/reset", routerDependencies.ConversationReset.HandleReset)
	multiplexer.HandleFunc("GET /admin/api/workspace/list", routerDependencies.WorkspaceFilesHandler.HandleList)
	multiplexer.HandleFunc("GET /admin/api/workspace/download", routerDependencies.WorkspaceFilesHandler.HandleDownload)
	multiplexer.HandleFunc("GET /admin/api/backup/manifest", routerDependencies.BackupHandler.HandleManifest)
	multiplexer.HandleFunc("POST /admin/api/backup/prepare", routerDependencies.BackupHandler.HandlePrepare)
	multiplexer.HandleFunc("POST /admin/api/backup/complete", routerDependencies.BackupHandler.HandleComplete)

	multiplexer.HandleFunc("GET /tasks/api/list", routerDependencies.TaskInboxHandler.HandleListOwnTaskRun)
	multiplexer.HandleFunc("GET /tasks/api/detail", routerDependencies.TaskInboxHandler.HandleGetOwnTaskRun)
	multiplexer.HandleFunc("POST /tasks/api/cancel", routerDependencies.TaskActionHandler.HandleCancelOwnTaskRun)
	multiplexer.HandleFunc("GET /tasks/api/events", routerDependencies.SSEHandler.HandleTaskEventStream)
	multiplexer.HandleFunc("GET /agent/api/replies", routerDependencies.AgentReplyHandler.HandleListReplies)
	if routerDependencies.ConnectorEventHandler != nil {
		multiplexer.HandleFunc("POST /connectors/mattermost/events", routerDependencies.ConnectorEventHandler.HandleConnectorEvent("mattermost"))
		multiplexer.HandleFunc("POST /connectors/slack/events", routerDependencies.ConnectorEventHandler.HandleConnectorEvent("slack"))
		multiplexer.HandleFunc("POST /connectors/signal/events", routerDependencies.ConnectorEventHandler.HandleConnectorEvent("signal"))
		multiplexer.HandleFunc("POST /connectors/api/events", routerDependencies.ConnectorEventHandler.HandleConnectorEvent("api"))
		multiplexer.HandleFunc("POST /connectors/buzz/events", routerDependencies.ConnectorEventHandler.HandleConnectorEvent("buzz"))
	}

	if _, errorValue := os.Stat("web/admin"); errorValue == nil {
		multiplexer.Handle("/_app/", AdminAssetHandler{RootDirectoryPath: "web/admin"})
		multiplexer.Handle("/admin/", http.StripPrefix("/admin/", AdminAssetHandler{RootDirectoryPath: "web/admin"}))
		multiplexer.Handle("/tasks/", TaskInboxHandler{RootDirectoryPath: "web/admin"})
		multiplexer.Handle("/login/", TaskInboxHandler{RootDirectoryPath: "web/admin"})
	}

	return withRecovery(withOriginCheck(multiplexer))
}
