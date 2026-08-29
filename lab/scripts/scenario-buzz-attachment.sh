#!/usr/bin/env bash
set -euo pipefail

# A person sends the agent a picture on Buzz and asks about it. The agent has to
# open the file. Everything before this scenario existed only for Mattermost, so
# the path that actually carries a company's messages was never driven here.
#
# The failure it exists for: the bridge that fetches an attachment runs beside
# the workspace image rather than inside it, wrote its copy on the host, and
# answered with the /workspace path the agent reads by. image_read then failed
# with "no such file or directory" on a file the agent had just been told about.

sudo_password="$1"
mount_directory_path="${3:-/mnt/shared/workspace}"

test -n "$sudo_password"

blueclaw_url=http://127.0.0.1:8080
chatd_url=http://172.31.0.1:18090
policy_path=/var/lib/blueclaw/delivery/config/policy.json
prompt="이 그림에 적힌 글자를 그대로 알려줘."
evidence_directory_path="$mount_directory_path/.artifacts/buzz-attachment"
mkdir -p "$evidence_directory_path"

run_as_root() {
  if [ "$(id -u)" = 0 ]; then
    "$@"
    return
  fi
  printf '%s\n' "$sudo_password" | sudo -S "$@"
}

phase() {
  echo "--- $1"
}

post_json() {
  local url="$1"
  local body="$2"
  curl --silent --show-error --fail-with-body -H 'Content-Type: application/json' -d "$body" "$url"
}

phase "wait for blueclaw and chatd"
for _ in $(seq 1 300); do
  if curl -fsS --max-time 3 "$blueclaw_url/admin/api/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS --max-time 5 "$blueclaw_url/admin/api/health" >/dev/null
for _ in $(seq 1 120); do
  if curl -fsS --max-time 3 "$chatd_url/healthz" >/dev/null 2>&1; then break; fi
  sleep 1
done

# The guest reads its policy from a share the host holds read-only, so the agent
# cannot invite anybody into it. The scenario is one of the people the device was
# provisioned with instead of one it makes up.
phase "take a person the device already has"
person_document="$(run_as_root cat "$policy_path" | jq -c '[.people[] | select((.emails // []) | length > 0)] | .[0]')"
test_person_identifier="$(printf '%s' "$person_document" | jq -r '.personID')"
test_email="$(printf '%s' "$person_document" | jq -r '.emails[0]')"
test -n "$test_person_identifier"
test -n "$test_email"
echo "sending as $test_email ($test_person_identifier)"

# The same formula buzzidentity.Secret uses: a person's key is their email under
# the device seed, so a scenario can be them without being handed a credential.
phase "derive their buzz secret from the device seed"
buzz_seed="$(run_as_root cat /root/.internkim/secrets/buzz-key-seed)"
test -n "$buzz_seed"
actor_secret="$(printf '%s' "$buzz_seed|secret|$test_email" | sha256sum | awk '{print $1}')"
actor="$(jq -cn --arg secret "$actor_secret" '{kind:"buzz-secret",secret:$secret}')"

# dm.ensure with no counterpart opens the conversation with the agent, so the
# scenario never has to know the bot's key.
phase "open the conversation with the agent"
conversation_identifier="$(post_json "$chatd_url/v1/platform/buzz/dm.ensure" "$(jq -cn \
  --arg userSecretHex "$actor_secret" \
  '{userSecretHex:$userSecretHex}')" | jq -r '.channelID // empty')"
test -n "$conversation_identifier"

phase "send the picture"
picture_base64="iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
sent_document="$(post_json "$chatd_url/v1/platform/buzz/person.message.send" "$(jq -cn \
  --argjson actor "$actor" \
  --arg conversationID "$conversation_identifier" \
  --arg body "$prompt" \
  --arg contentBase64 "$picture_base64" \
  '{actor:$actor,conversationID:$conversationID,body:$body,attachments:[{filename:"buzz-attachment-test.png",contentType:"image/png",contentBase64:$contentBase64}]}')")"
printf '%s' "$sent_document" > "$evidence_directory_path/sent.json"

phase "wait for the task"
task_run_identifier=""
for _ in $(seq 1 240); do
  task_run_identifier="$(curl -fsS --max-time 5 "$blueclaw_url/admin/api/run" \
    | jq -r --arg personID "$test_person_identifier" '[.[] | select(.requesterPersonID == $personID)] | .[0].taskRunID // empty')"
  if [ -n "$task_run_identifier" ]; then break; fi
  sleep 2
done
test -n "$task_run_identifier"
echo "task run: $task_run_identifier"

for _ in $(seq 1 240); do
  status="$(curl -fsS --max-time 5 "$blueclaw_url/admin/api/run" \
    | jq -r --arg taskRunID "$task_run_identifier" '[.[] | select(.taskRunID == $taskRunID)] | .[0].status // empty')"
  case "$status" in
    completed|failed|cancelled) break ;;
  esac
  sleep 2
done

phase "read the ledger"
detail_document="$(curl -fsS --max-time 15 "$blueclaw_url/admin/api/run/detail?taskRunID=$task_run_identifier")"
printf '%s' "$detail_document" > "$evidence_directory_path/run-detail.json"

reads="$(printf '%s' "$detail_document" \
  | jq -c '[.taskEvents[].body | select(type == "object") | select(.tool == "read")]')"
if [ "$(printf '%s' "$reads" | jq 'length')" = "0" ]; then
  echo "✗ the agent never tried to open the picture it was sent"
  exit 1
fi

missing="$(printf '%s' "$reads" | jq -c '[.[] | select((.failure.code // "") == "not_found")]')"
if [ "$(printf '%s' "$missing" | jq 'length')" != "0" ]; then
  echo "✗ the agent was told about a file that was not there:"
  printf '%s' "$missing" | jq -r '.[0].output.content // .[0].failure.userSafeSummary // ""'
  exit 1
fi

if [ "$(printf '%s' "$reads" | jq '[.[] | select(.failure == null)] | length')" = "0" ]; then
  echo "✗ every attempt to open the picture failed:"
  printf '%s' "$reads" | jq -r '.[0].output.content // ""'
  exit 1
fi

echo "✓ buzz attachment: the agent opened the picture it was sent"
