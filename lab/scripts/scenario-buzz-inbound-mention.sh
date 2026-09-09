#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
mount_directory_path="${3:-/mnt/shared/workspace}"
blueclaw_url=http://127.0.0.1:8080
chatd_url=http://172.31.0.1:18090
policy_path=/var/lib/blueclaw/delivery/config/policy.json
seed_path=/root/.internkim/secrets/buzz-key-seed
evidence_directory_path="$mount_directory_path/.artifacts/buzz-inbound-mention"
mkdir -p "$evidence_directory_path"

run_as_root() {
  if [ "$(id -u)" = 0 ]; then
    "$@"
    return
  fi
  printf '%s\n' "$sudo_password" | sudo -S "$@"
}

post_json() {
  local url="$1"
  local body="$2"
  curl --silent --show-error --fail-with-body \
    -H 'Content-Type: application/json' \
    -d "$body" \
    "$url"
}

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/scenario-buzz-people.sh"

for _ in $(seq 1 300); do
  curl -fsS --max-time 3 "$blueclaw_url/admin/api/health" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS --max-time 5 "$blueclaw_url/admin/api/health" >/dev/null
for _ in $(seq 1 120); do
  curl -fsS --max-time 3 "$chatd_url/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS --max-time 5 "$chatd_url/healthz" >/dev/null

people_document="$(company_people_with_buzz_identity)"
sender_email="$(printf '%s' "$people_document" | jq -r '.[0].emails[0] // empty')"
test -n "$sender_email"
sender_seed="$(run_as_root cat "$seed_path")"
sender_secret="$(printf '%s|secret|%s' "$sender_seed" "$sender_email" | sha256sum | awk '{print $1}')"
bot_pubkey="$(post_json "$chatd_url/v1/platform/buzz/identity.self" '{}' | jq -er '.pubkeyHex | select(test("^[0-9a-f]{64}$"))')"
test -n "$bot_pubkey"

channel_name="fleet-inbound-mention-$(date +%s)"
channel_request="$(jq -cn --arg name "$channel_name" '{name:$name,description:"disposable inbound mention probe"}')"
if ! channel_document="$(post_json "$chatd_url/v1/platform/buzz/channel.ensure" "$channel_request")"; then
  echo "✗ buzz inbound mention: chatd could not create channel $channel_name" >&2
  exit 1
fi
channel_id="$(printf '%s' "$channel_document" | jq -r '.channelID // empty')"
if [ -z "$channel_id" ]; then
  echo "✗ buzz inbound mention: channel.ensure returned no channelID: $channel_document" >&2
  exit 1
fi

agent_secret="$(printf '%s|secret|__agent__' "$sender_seed" | sha256sum | awk '{print $1}')"
if ! membership_document="$(cd "$mount_directory_path/.dependency/blueclaw/chatd" && \
  AGENT_SECRET="$agent_secret" SENDER_SECRET="$sender_secret" RELAY_URL=ws://127.0.0.1:3000 CHANNEL_ID="$channel_id" \
  bun -e 'import { createBuzzRelayClient } from "./src/adapters/buzz/relay-client.ts"; const agent = createBuzzRelayClient(process.env.RELAY_URL, process.env.AGENT_SECRET); const sender = createBuzzRelayClient(process.env.RELAY_URL, process.env.SENDER_SECRET); await agent.connect(); await agent.publishForAcknowledgement(9000, "", [["h", process.env.CHANNEL_ID], ["p", agent.pubkeyHex], ["role", "owner"]]); const acknowledgement = await agent.publishForAcknowledgement(9000, "", [["h", process.env.CHANNEL_ID], ["p", sender.pubkeyHex]]); console.log(JSON.stringify({senderPubkey: sender.pubkeyHex, acknowledgement})); agent.disconnect();')"; then
  echo "✗ buzz inbound mention: agent could not add $sender_email to channel $channel_id" >&2
  exit 1
fi
sender_pubkey="$(printf '%s' "$membership_document" | jq -r '.senderPubkey // empty')"
if [ -z "$sender_pubkey" ]; then
  echo "✗ buzz inbound mention: membership event returned no sender pubkey: $membership_document" >&2
  exit 1
fi
message_id="$(cd "$mount_directory_path/.dependency/blueclaw/chatd" && \
  SENDER_SECRET="$sender_secret" RELAY_URL=ws://127.0.0.1:3000 CHANNEL_ID="$channel_id" BOT_PUBKEY="$bot_pubkey" \
  bun -e 'import { createBuzzRelayClient } from "./src/adapters/buzz/relay-client.ts"; const relay = createBuzzRelayClient(process.env.RELAY_URL, process.env.SENDER_SECRET); await relay.connect(); const event = await relay.publish(9, "fleet p-tag ingress probe", [["h", process.env.CHANNEL_ID], ["p", process.env.BOT_PUBKEY]]); console.log(event.id); relay.disconnect();')"
test -n "$message_id"
printf '%s\n' "$message_id" > "$evidence_directory_path/message-id"

diagnostic=''
for _ in $(seq 1 60); do
  diagnostic="$(curl --silent --show-error --fail "$blueclaw_url/admin/api/connector/events?platform=buzz&messageID=$message_id&limit=1")"
  if printf '%s' "$diagnostic" | jq -e --arg message_id "$message_id" 'any(.[]; .externalMessageID == $message_id)' >/dev/null; then
    printf '%s\n' "$diagnostic" > "$evidence_directory_path/diagnostic.json"
    echo "✓ buzz inbound mention: connector recorded $message_id for $sender_email"
    exit 0
  fi
  sleep 1
done
printf '%s\n' "$diagnostic" > "$evidence_directory_path/diagnostic.json"
echo "✗ buzz inbound mention: connector did not record $message_id" >&2
exit 1
