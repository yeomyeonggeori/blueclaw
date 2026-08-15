#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
mattermost_listen_address="$2"
mount_directory_path="$3"
evidence_directory_path="${4:-$mount_directory_path/.artifacts/workspace-persistence}"
blueclaw_url=http://127.0.0.1:8080
workspace_image=/var/lib/blueclaw/workspace.ext4
host_workspace=/root/.blueclaw/workspace

test -n "$sudo_password"
test -n "$mattermost_listen_address"
test -d "$mount_directory_path"
mkdir -p "$evidence_directory_path"

run_as_root() {
  if [ "$(id -u)" = 0 ]; then
    "$@"
    return
  fi
  printf '%s\n' "$sudo_password" | sudo -S "$@"
}

wait_for_blueclaw() {
  for _ in $(seq 1 300); do
    if curl -fsS --max-time 3 "$blueclaw_url/admin/api/health" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  return 1
}

requester_person_id="$(curl -fsS "$blueclaw_url/admin/api/policy" | jq -er '.people[0].personID')"
task_response="$(curl -fsS --max-time 480 -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg person "$requester_person_id" '{requesterPersonID:$person,requesterName:"Workspace Persistence Regression",conversationID:"regression:workspace-persistence",prompt:"Reply with exactly persistence-ok."}')" \
  "$blueclaw_url/admin/api/task/run")"
task_run_id="$(jq -er '.taskRun.taskRunID' <<<"$task_response")"

cleanup() {
  run_as_root systemctl start blueclaw >/dev/null 2>&1 || true
  wait_for_blueclaw >/dev/null 2>&1 || true
  curl -fsS -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg task "$task_run_id" '{taskRunID:$task,viewerIsAdmin:true}')" \
    "$blueclaw_url/admin/api/task/delete" >/dev/null 2>&1 || true
}
trap cleanup EXIT

before_detail="$(curl -fsS "$blueclaw_url/admin/api/task/detail?taskRunID=$task_run_id")"
jq -e '.taskRun.currentAttemptID | length > 0' <<<"$before_detail" >/dev/null
jq -e '.taskEvents | length > 0' <<<"$before_detail" >/dev/null
before_detail_digest="$(jq -S . <<<"$before_detail" | sha256sum | awk '{print $1}')"
before_task_run_row_count=1
before_attempt_row_count="$(jq '[.taskRun.currentAttemptID | select(length > 0)] | length' <<<"$before_detail")"
before_task_step_row_count="$(jq '.taskSteps | length' <<<"$before_detail")"
before_task_event_row_count="$(jq '.taskEvents | length' <<<"$before_detail")"
before_schedules="$(curl -fsS "$blueclaw_url/admin/api/task-schedules?includeExpired=true&pageSize=200")"
before_schedule_digest="$(jq -S 'del(.checkedAt)' <<<"$before_schedules" | sha256sum | awk '{print $1}')"
before_schedule_row_count="$(jq '.totalCount' <<<"$before_schedules")"
printf '%s\n' "$before_detail" >"$evidence_directory_path/task-detail-before.json"
printf '%s\n' "$before_schedules" >"$evidence_directory_path/schedules-before.json"

run_as_root systemctl stop blueclaw
before_pg_version="$(run_as_root debugfs -R 'cat /.blueclaw/postgres/data/PG_VERSION' "$workspace_image" 2>/dev/null | tr -d '\r\n')"
test -n "$before_pg_version"

run_as_root mkdir -p /var/lib/blueclaw/delivery/runtime/current /var/lib/blueclaw/delivery/skills
run_as_root rsync -a --delete "$host_workspace/.blueclaw/runtime/current/" /var/lib/blueclaw/delivery/runtime/current/
run_as_root rsync -a --delete "$host_workspace/skills/" /var/lib/blueclaw/delivery/skills/

after_sync_pg_version="$(run_as_root debugfs -R 'cat /.blueclaw/postgres/data/PG_VERSION' "$workspace_image" 2>/dev/null | tr -d '\r\n')"
if [ "$after_sync_pg_version" != "$before_pg_version" ]; then
  echo "PG_VERSION changed across a delivery refresh: before=$before_pg_version after=$after_sync_pg_version" >&2
  exit 1
fi

run_as_root systemctl start blueclaw
wait_for_blueclaw
after_detail="$(curl -fsS "$blueclaw_url/admin/api/task/detail?taskRunID=$task_run_id")"
after_detail_digest="$(jq -S . <<<"$after_detail" | sha256sum | awk '{print $1}')"
after_task_run_row_count=1
after_attempt_row_count="$(jq '[.taskRun.currentAttemptID | select(length > 0)] | length' <<<"$after_detail")"
after_task_step_row_count="$(jq '.taskSteps | length' <<<"$after_detail")"
after_task_event_row_count="$(jq '.taskEvents | length' <<<"$after_detail")"
after_schedules="$(curl -fsS "$blueclaw_url/admin/api/task-schedules?includeExpired=true&pageSize=200")"
after_schedule_digest="$(jq -S 'del(.checkedAt)' <<<"$after_schedules" | sha256sum | awk '{print $1}')"
after_schedule_row_count="$(jq '.totalCount' <<<"$after_schedules")"
printf '%s\n' "$after_detail" >"$evidence_directory_path/task-detail-after.json"
printf '%s\n' "$after_schedules" >"$evidence_directory_path/schedules-after.json"
if [ "$after_detail_digest" != "$before_detail_digest" ]; then
  echo "task/attempt/event digest changed: before=$before_detail_digest after=$after_detail_digest" >&2
  exit 1
fi
if [ "$after_schedule_digest" != "$before_schedule_digest" ]; then
  echo "schedule digest changed: before=$before_schedule_digest after=$after_schedule_digest" >&2
  exit 1
fi
before_row_counts="$before_task_run_row_count:$before_attempt_row_count:$before_task_step_row_count:$before_task_event_row_count:$before_schedule_row_count"
after_row_counts="$after_task_run_row_count:$after_attempt_row_count:$after_task_step_row_count:$after_task_event_row_count:$after_schedule_row_count"
if [ "$after_row_counts" != "$before_row_counts" ]; then
  echo "task/attempt/step/event/schedule row counts changed: before=$before_row_counts after=$after_row_counts" >&2
  exit 1
fi

after_restart_pg_version="$(run_as_root debugfs -R 'cat /.blueclaw/postgres/data/PG_VERSION' "$workspace_image" 2>/dev/null | tr -d '\r\n')"
if [ "$after_restart_pg_version" != "$before_pg_version" ]; then
  echo "PG_VERSION changed across restart: before=$before_pg_version after=$after_restart_pg_version" >&2
  exit 1
fi

jq -n \
  --arg taskRunID "$task_run_id" \
  --arg beforeDigest "$before_detail_digest" \
  --arg afterDigest "$after_detail_digest" \
  --arg beforeScheduleDigest "$before_schedule_digest" \
  --arg afterScheduleDigest "$after_schedule_digest" \
  --argjson beforeTaskRunRowCount "$before_task_run_row_count" \
  --argjson beforeAttemptRowCount "$before_attempt_row_count" \
  --argjson beforeTaskStepRowCount "$before_task_step_row_count" \
  --argjson beforeTaskEventRowCount "$before_task_event_row_count" \
  --argjson beforeScheduleRowCount "$before_schedule_row_count" \
  --argjson afterTaskRunRowCount "$after_task_run_row_count" \
  --argjson afterAttemptRowCount "$after_attempt_row_count" \
  --argjson afterTaskStepRowCount "$after_task_step_row_count" \
  --argjson afterTaskEventRowCount "$after_task_event_row_count" \
  --argjson afterScheduleRowCount "$after_schedule_row_count" \
  --arg beforePGVersion "$before_pg_version" \
  --arg afterSyncPGVersion "$after_sync_pg_version" \
  --arg afterRestartPGVersion "$after_restart_pg_version" \
  '{taskRunID:$taskRunID,beforeDigest:$beforeDigest,afterDigest:$afterDigest,beforeScheduleDigest:$beforeScheduleDigest,afterScheduleDigest:$afterScheduleDigest,beforeRowCounts:{taskRun:$beforeTaskRunRowCount,attemptReference:$beforeAttemptRowCount,taskStep:$beforeTaskStepRowCount,taskEvent:$beforeTaskEventRowCount,taskSchedule:$beforeScheduleRowCount},afterRowCounts:{taskRun:$afterTaskRunRowCount,attemptReference:$afterAttemptRowCount,taskStep:$afterTaskStepRowCount,taskEvent:$afterTaskEventRowCount,taskSchedule:$afterScheduleRowCount},beforePGVersion:$beforePGVersion,afterSyncPGVersion:$afterSyncPGVersion,afterRestartPGVersion:$afterRestartPGVersion}' \
  >"$evidence_directory_path/evidence.json"

echo "workspace-persistence: ok taskRunID=$task_run_id taskDigest=$before_detail_digest scheduleDigest=$before_schedule_digest rowCounts=$before_row_counts PG_VERSION=$before_pg_version"
