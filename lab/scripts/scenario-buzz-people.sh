#!/usr/bin/env bash

company_people_with_buzz_identity() {
  local member_emails
  member_emails="$(run_as_root sh -c '
    curl -fsS --max-time 10 \
      -H "Authorization: Bearer $(cat /root/.internkim/secrets/central-plane-agent-key)" \
      "$(cat /root/.internkim/env/central-plane-app-url)/api/agent/member"
  ' | jq -r '.members[] | select((.status // "") == "active") | .email')"
  run_as_root cat "$policy_path" | jq -c --arg emails "$member_emails" '
    ($emails | split("\n") | map(select(length > 0))) as $atTheCompany
    | [.people[] | select((.emails // []) | length > 0) | select(.emails[0] as $email | $atTheCompany | index($email))]
  '
}
