from __future__ import annotations

import json
import threading
from datetime import datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

DEMO_TASK_RUNS = [
    ("tr_9f2a", "waiting_approval", "Ada", 19.5, "Send the Q3 rollout summary to the ops channel"),
    ("tr_7c14", "running", "Grace", 19.5, "Draft the incident review for yesterday's outage"),
    ("tr_5b83", "completed", "Linus", 22.5, "Find every service still on the old capability schema"),
    ("tr_31de", "blocked", "Grace", 25.5, "Publish the changelog to the docs site"),
    ("tr_20a7", "failed", "Rob", 27.5, "Reconcile last month's invoices"),
]

DEMO_TIMELINE_START = (23, 55, 15)

DEMO_TASK_EVENTS = [
    (0, "agent.task_launched", {}),
    (20, "tool.skill_search.requested", {"toolName": "skill_search"}),
    (20, "tool.skill_search.result", {"summary": "selected the messaging skill"}),
    (38, "tool.conversation_history.requested", {"toolName": "conversation_history"}),
    (38, "tool.conversation_history.result", {"summary": "read 20 messages from #ops"}),
    (74, "tool.message_send.requested", {"toolName": "message_send"}),
    (75, "approval.hold_opened", {"toolName": "message_send", "confirmation": "Post the Q3 rollout summary to #ops?"}),
]

DEMO_HARNESS = {
    "name": "bluecollar",
    "runsAsRequesterIdentity": True,
    "toolCatalogURL": "http://127.0.0.1:8081/harness/tool-catalog",
}


def local_timestamp(moment: datetime) -> str:
    return moment.astimezone().isoformat(timespec="seconds")


def task_run_payload() -> list[dict]:
    now = datetime.now().astimezone()
    return [
        {
            "taskRunID": task_run_id,
            "requesterPersonID": "person_" + display_name.lower(),
            "requesterDisplayName": display_name,
            "originConversationID": "conv_ops",
            "currentAgentProfileName": "blueclaw",
            "status": status,
            "prompt": prompt,
            "result": "",
            "failureReason": "",
            "createdAt": local_timestamp(now - timedelta(hours=age_hours)),
            "updatedAt": local_timestamp(now),
        }
        for task_run_id, status, display_name, age_hours, prompt in DEMO_TASK_RUNS
    ]


def task_event_payload(task_run_id: str) -> list[dict]:
    hour, minute, second = DEMO_TIMELINE_START
    timeline_start = datetime.now().astimezone().replace(hour=hour, minute=minute, second=second, microsecond=0)
    return [
        {
            "taskEventID": f"ev_{event_index:03d}",
            "taskRunID": task_run_id,
            "name": event_name,
            "body": json.dumps(event_body),
            "createdAt": local_timestamp(timeline_start + timedelta(seconds=offset_seconds)),
        }
        for event_index, (offset_seconds, event_name, event_body) in enumerate(DEMO_TASK_EVENTS)
    ]


def task_run_detail_payload(task_run_id: str) -> dict:
    matching = [task_run for task_run in task_run_payload() if task_run["taskRunID"] == task_run_id]
    if not matching:
        return {"taskRun": {}, "taskEvents": []}
    return {"taskRun": matching[0], "taskEvents": task_event_payload(task_run_id)}


class FixtureAPIHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/admin/api/task/detail"):
            return self.respond(task_run_detail_payload(self.requested_task_run_id()))
        if self.path.startswith("/admin/api/task"):
            return self.respond(task_run_payload())
        if self.path.startswith("/admin/api/harness"):
            return self.respond(DEMO_HARNESS)
        self.send_error(404)

    def requested_task_run_id(self) -> str:
        return parse_qs(urlparse(self.path).query).get("taskRunID", [""])[0]

    def respond(self, payload) -> None:
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *arguments) -> None:
        pass


def serve_fixture_api(port: int) -> ThreadingHTTPServer:
    server = ThreadingHTTPServer(("127.0.0.1", port), FixtureAPIHandler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server
