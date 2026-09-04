#!/usr/bin/env bash
set -euo pipefail

# Somebody asks the agent, through the public API, to send a colleague a direct
# message — the shape the internkim-api skill produces. The colleague then reads
# their own Buzz inbox and has to find it there.
#
# The failure it exists for: capabilityd chose its messenger from a string the
# caller sent, the public API had no conversation to name and sent the name of
# its own door instead, and the direct message left on Mattermost while
# answering "sent". Every existing direct-message scenario drives Mattermost, so
# the path a company's messages actually travel was never checked outbound.

sudo_password="$1"
mount_directory_path="${3:-/mnt/shared/workspace}"

test -n "$sudo_password"

blueclaw_url=http://127.0.0.1:8080
chatd_url=http://172.31.0.1:18090
admind_socket_path=/run/internkim/admind.sock
policy_path=/var/lib/blueclaw/delivery/config/policy.json
seed_path=/root/.internkim/secrets/buzz-key-seed
evidence_directory_path="$mount_directory_path/.artifacts/buzz-direct-message"
marker="buzz dm through the api $(date +%s)"
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

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/scenario-buzz-people.sh"

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
if ! curl -fsS --max-time 5 "$chatd_url/healthz" >/dev/null; then
  echo "✗ chatd never answered $chatd_url/healthz; every call below would fail on the same thing" >&2
  exit 1
fi

phase "take two people the company says work here"
people_document="$(company_people_with_buzz_identity)"
colleague_count="$(printf '%s' "$people_document" | jq 'length')"
if [ "$colleague_count" -lt 2 ]; then
  echo "✗ the company and the device agree on $colleague_count people; this scenario needs a requester and a recipient" >&2
  exit 1
fi
requester_email="$(printf '%s' "$people_document" | jq -r '.[0].emails[0]')"
recipient_document="$(printf '%s' "$people_document" | jq -c '.[1]')"
recipient_email="$(printf '%s' "$recipient_document" | jq -r '.emails[0]')"
recipient_name="$(printf '%s' "$recipient_document" | jq -r '.name // .displayName // ""')"
echo "$requester_email asks the agent to write to $recipient_email"

phase "ask through the public API, the way the skill does"
invoke_body="$(jq -cn \
  --arg personHint "$recipient_email" \
  --arg message "$marker" \
  '{input:{targetType:"directMessage",personHint:$personHint,message:$message}}')"
invoke_document="$(run_as_root curl --silent --show-error --fail-with-body \
  --unix-socket "$admind_socket_path" \
  -X POST "http://internkim/api/v1/tools/message_send/invoke" \
  -H 'Content-Type: application/json' \
  -H "X-INTERNKIM-REQUESTER-EMAIL: $requester_email" \
  -H 'X-INTERNKIM-REQUESTER-PERMISSION: write' \
  -d "$invoke_body")"
printf '%s' "$invoke_document" > "$evidence_directory_path/invoke.json"
echo "$invoke_document"

if [ "$(printf '%s' "$invoke_document" | jq -r '.isError // false')" = "true" ]; then
  echo "✗ the api refused to send: $(printf '%s' "$invoke_document" | jq -r '.message // ""')"
  exit 1
fi

# The same formula buzzidentity.Secret uses: a person's key is their email under
# the device seed, so the scenario can read as them without being handed a
# credential.
phase "read the recipient's own buzz inbox"
buzz_seed="$(run_as_root cat "$seed_path")"
test -n "$buzz_seed"
recipient_secret="$(printf '%s' "$buzz_seed|secret|$recipient_email" | sha256sum | awk '{print $1}')"
recipient_actor="$(jq -cn --arg secret "$recipient_secret" '{kind:"buzz-secret",secret:$secret}')"

sender_pubkey="$(run_as_root curl --silent --fail \
  --unix-socket "$admind_socket_path" \
  -X POST http://internkim/admin/api/directory/buzz-key \
  -H 'Content-Type: application/json' \
  -d "$(jq -cn --arg email "$requester_email" '{email:$email}')" | jq -r '.pubkeyHex // empty')"
test -n "$sender_pubkey"

conversation_identifier="$(post_json "$chatd_url/v1/platform/buzz/dm.ensure" "$(jq -cn \
  --arg userSecretHex "$recipient_secret" \
  --arg counterpartPubkeyHex "$sender_pubkey" \
  '{userSecretHex:$userSecretHex,counterpartPubkeyHex:$counterpartPubkeyHex}')" | jq -r '.channelID // empty')"
test -n "$conversation_identifier"

messages_document=""
delivered=""
for _ in $(seq 1 60); do
  messages_document="$(post_json "$chatd_url/v1/platform/buzz/person.messages.list" "$(jq -cn \
    --argjson actor "$recipient_actor" \
    --arg conversationID "$conversation_identifier" \
    '{actor:$actor,conversationID:$conversationID}')")"
  delivered="$(printf '%s' "$messages_document" | jq -r --arg marker "$marker" \
    '[.. | objects | select(((.body? // .message? // "") | type) == "string") | select((.body? // .message? // "") | contains($marker))] | .[0] // empty')"
  if [ -n "$delivered" ]; then
    printf '%s' "$messages_document" > "$evidence_directory_path/recipient-inbox.json"
    break
  fi
  sleep 2
done

if [ -z "$delivered" ]; then
  printf '%s' "$messages_document" > "$evidence_directory_path/recipient-inbox.json"
  echo "✗ nothing carrying the marker reached ${recipient_name:-$recipient_email} on buzz"
  echo "  the api answered sent, so the message left on another messenger"
  exit 1
fi

echo "✓ buzz direct message: the api sent it and ${recipient_name:-$recipient_email} has it on buzz"
