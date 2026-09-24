# Overview

blueclaw is a self-hosted Go daemon that runs an AI agent on behalf of the several people who share one machine. It executes each requester's tool calls as that person's own unprivileged POSIX user, holds side-effecting calls at an approval gate, and writes every step of every task to a durable event ledger in Postgres.

> [!WARNING]
> blueclaw is pre-alpha. The interfaces, the wire grammar, the configuration keys and the database schema change without notice, and there is no release or upgrade path between commits. Pin a commit and expect to read diffs.

## Why it exists

A company that runs one agent on one machine usually runs it as one Unix account. Every person who talks to it then shares one home directory, one set of files and one view of every secret, and nothing records which person authorized which side effect. blueclaw makes those people different people to the operating system. The harness decides what to call; blueclaw decides who the call runs as, whether it runs at all, and what is written down.

## What it is

- **A host.** It owns connectors, identity, POSIX isolation, the task store, approvals, the tool catalog, capabilities, memory and delivery.
- **A harness socket.** The agent loop sits behind one Go method, `agentcontract.Harness.RunTurn`. The bundled loop is [bluecollar](https://github.com/yeomyeonggeori/bluecollar); an ACP agent, Claude Code, Codex or Antigravity can take its place.
- **POSIX as the boundary.** Ownership and mode bits decide what a tool call may touch. There is no executable allowlist, no denied path prefix and no prompt telling the model what it may not do.

## What it is not

It is not an agent, a model or a chat client. It does not sandbox the agent from the machine; it separates the people using the machine from each other, and a container or VM around it composes with that. There are no binary releases: you build it from source.

blueclaw is the agent host inside [InternKim](https://intern.kim).

## Where to go next

- [Quickstart](#quickstart) builds the daemon and drives a task over HTTP.
- [Architecture](#architecture) shows how host, harness and contract divide the work.
- [Concepts](#concepts) defines tasks, approvals, policy, skills and memory.
- [Boundaries](#boundaries) states the security model in detail.
- [Operations](#operations) covers configuration, the database and deployment.

# Quickstart

### Requirements

Go 1.26, [Bun](https://bun.sh) 1.3 for the TypeScript packages, Postgres, and a language model endpoint that speaks the OpenAI chat completions API. The bundled harness and the memory store are git submodules, so clone recursively:

```bash
git clone --recursive https://github.com/yeomyeonggeori/blueclaw.git
cd blueclaw
go build ./...
```

### Configure

`config/runtime.standalone.example.json` names every model, the database and the key as references such as `${BLUECLAW_MODEL}`, and the loader fills each one in from the environment at boot. A reference whose variable is unset stops the daemon and names the variable. `.monkeys` at the repository root holds the values:

```ini
@standalone
OPENROUTER_API_KEY
BLUECLAW_MODEL_ENDPOINT=https://openrouter.ai/api/v1
BLUECLAW_MODEL=z-ai/glm-5.3-flash
BLUECLAW_EMBEDDING_MODEL=baai/bge-m3
BLUECLAW_DECISION_ENDPOINT=https://openrouter.ai/api/alpha/decisions
BLUECLAW_DECISION_MODEL=~typesafe/jev-latest
BLUECLAW_DATABASE_URL=postgres://blueclaw:blueclaw@127.0.0.1:5432/blueclaw?sslmode=disable
```

[monkeys](https://github.com/eastriverlee/monkeys) keeps `OPENROUTER_API_KEY` in the operating system's keychain (`monkeys remember @standalone OPENROUTER_API_KEY` stores it once) and sets all of them for one command. Without it, export the same variables. A model entry names the variable that holds its key with `apiKeyEnvironment`; `apiKeyPath` reads a key file instead, and an entry may name only one of the two.

One OpenRouter key reaches all three models. The decision model answers intake's closed questions. [Kev](https://github.com/jaredpalmer/kev) serves the same API on your own machine: point `BLUECLAW_DECISION_ENDPOINT` at its `/v1/systemone` and set `BLUECLAW_DECISION_MODEL` to `kev-latest`. A 4B chat model served locally is not enough, since intake asks the chat model for answers in a fixed schema that small models break.

Copy `config/policy.example.json` to `policy.json` and add the people who may use the daemon. Each person needs a `personID`, `emails`, and the `circles` they belong to:

```json
{
  "personID": "00000000-0000-0000-0000-000000000002",
  "displayName": "Alex",
  "emails": ["sample@example.com"],
  "securityLevelName": "member",
  "securityLevelRank": 10,
  "circles": ["member"]
}
```

`company.timeZone` in the same file sets the clock that schedules and relative dates read.

### Start the daemon

```bash
monkeys run go run ./cmd/blueclaw --runtime config/runtime.standalone.example.json --policy policy.json
curl -s localhost:8081/admin/api/health | jq '.status, .languageModel, .protocolIdentity.passed'
```

Without `--runtime` and `--policy` the daemon reads `runtime.json` and `policy.json` from `$BLUECLAW_HOME`, then `$XDG_CONFIG_HOME/blueclaw`, then `~/.blueclaw`. The embedded migrations run at boot. The listen address comes from `baseURL` (the standalone example uses port 8081; with no `baseURL` it is `127.0.0.1:8080`).

A model that fails to initialize shows in `languageModel.error`; health answers 503 and the task workers stay stopped while the HTTP surface stays up for diagnosis.

### Turn on per-person isolation

Until `terminal.posixHelperPath` is set, the daemon cannot act as anyone, and tools that touch the workspace as the requester fail closed. Build and install the setuid helper:

```bash
go build -o /usr/local/bin/blueclaw-posix-helper ./cmd/blueclaw-posix-helper
sudo chown root:root /usr/local/bin/blueclaw-posix-helper
sudo chmod 4755 /usr/local/bin/blueclaw-posix-helper
```

Set `terminal.posixHelperPath` to that path and restart. The daemon then creates one Linux user per person and one group per circle at every boot. An external harness (`acp`, `claude-code`, `codex`, `antigravity`) refuses to run without this step.

### Send work as two people

The `api` connector needs no chat platform. Address two people from your policy by email:

```bash
for sender in sample@example.com example@example.com; do
  curl -s -X POST localhost:8081/connectors/api/events -H 'content-type: application/json' \
    -d "{\"conversationID\":\"dm:api:$sender\",\"messageID\":\"m1\",\"senderID\":\"$sender\",
         \"replyTargetID\":\"dm:api:$sender\",\"prompt\":\"Write your name to a file in your home directory.\"}"
done

curl -s 'localhost:8081/agent/api/replies?conversationID=dm:api:sample@example.com'
```

The two runs execute as different Linux users with different `0700` home directories, and neither can read the other's file.

> [!NOTE]
> Intake asks a decision model whether and how to answer each inbound message, and that model is named only by `languageModel.capability.decisionModel`. A configuration that names tiers alone has no decision model, and intake reports `intake decision model unavailable`. Running end to end needs a capability service; see [Capabilities](#capabilities).

### Watch it from a terminal

```bash
go build -o blueclaw-cli ./cmd/blueclaw-cli
./blueclaw-cli --base-url http://127.0.0.1:8081
```

`blueclaw-cli` reads the admin API. Its four screens (switched with `1`–`4` or `tab`) list task runs, replay one run from its ledger, list the approvals waiting on a person (`y` confirms the held call, `a` confirms every held call in that task, `n` cancels), and report which harness is running and whether calls run as the requester. Run without an enrolled daemon, it walks through setup and can manage a local Postgres for you (`internal/enrollment`).

# Architecture

blueclaw is split into three parts that compile against each other.

| Part | Owns | Where |
|---|---|---|
| Host (blueclaw) | connectors, policy, identity, POSIX isolation, task store, approvals, tool catalog, capabilities, memory, delivery | this repository |
| Harness | the agent loop: run a turn and report what happened | bluecollar at `.dependency/bluecollar`, or an external agent through `internal/acpharness` and `internal/cliharness` |
| Contract | the types both sides compile against, and the harness port | `agentcontract` and `toolcontract` in the bluecollar module |

bluecollar is a separate repository pinned as a submodule and pulled in through a `replace` directive in `go.mod`. `agentcontract` travels with it because both sides need identical types. Building with `-tags nobundledharness` leaves the loop out of the binary; `agent.harness.name` must then name an external harness, and a test in `cmd/blueclaw` fails if the loop creeps back in.

```text
  chat platform (via chatd) / HTTP / ACP client
            |
            v
  blueclaw daemon
    connectors · intake · policy · task store · approvals · tool catalog · POSIX projection
            |
            +-- agentcontract.Harness --+-- bluecollar (Go, in process)
            |                           +-- ACP or CLI agent, started as the requester
            |
            +-- tool execution --> blueclaw-posix-helper --> the requester's UID, GID and groups
```

### The harness port

```go
type Harness interface {
	RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
}
```

The host opens the task run and hands the harness an `ExistingTaskRunID` to settle, so a first turn is recorded the same way whichever loop ran it. Everything else the host needs (events, cancellation, run lookup) it takes from the task store.

Deciding whether an inbound message becomes a task at all is host policy. One call to the decision model answers every closed question about a message: who it is addressed to, whether it follows on from a running task, and how the turn should be routed. The answer is memoized on the inbound event (`internal/connectors/intake_decision.go`, implemented by bluecollar's `intake` package).

### The path of one message

1. **Ingress.** A connector persists the raw event, resolves the sender to a person in the policy, and refuses accounts the policy does not know.
2. **Decision.** Addressing is one of four outcomes: ignore, react only, reply, or react and reply. A message that is only an image is described by a vision model first.
3. **Busy routing.** If the person already has an active task, the decision's `busyRoute` is one of `status`, `steer`, `replace`, `cancel`, `new_task` or `unrelated`.
4. **Launch.** `TaskLauncher.Launch` (`internal/agentruntime/task_launcher.go`) runs nine steps, each recorded as an event so a failure names its step: resolve the requester's email, resolve the active circle, build the conversation's artifact manifest, provision the requester's workspace, build the tool set, audit the tool registry, load memory, carry out an approved call, run the turn.
5. **Delivery.** The result is enqueued in the connector outbox and delivered.

### Choosing a harness

`agent.harness.name` selects the loop, and `internal/harnessselection` is the only place a harness is named.

| Name | What runs |
|---|---|
| `bluecollar` (default) | the bundled in-process loop |
| `acp` | any [Agent Client Protocol](https://agentclientprotocol.com) agent at `agentCommandPath` |
| `claude-code` | the `claude` CLI in headless mode |
| `codex` | the `codex` CLI |
| `antigravity` | the `agy` CLI |

An external harness changes where the agent runs, how it reaches tools, and how the outcome is known. It starts inside the requester's POSIX identity. It reaches the catalog over MCP at `/harness/tool-catalog` with a session token revoked when the turn ends; agents that advertise MCP over HTTP get the endpoint directly, and the rest get a stdio server (the daemon binary in `mcp-tool-catalog` mode) that proxies to it. Because an ACP turn ending or a CLI exiting says nothing about whether the work was done, `internal/turnoutcome` decides the status from the agent's final message and the catalog tools that actually succeeded.

`internal/acpharness` also refuses ACP's own filesystem and terminal methods, which pushes file and shell work onto the catalog. Tools an agent runs inside its own process are answered yes and recorded as `harness.tool_permitted` or `harness.tool_refused`; the POSIX identity is the limit on those.

### Serving ACP

blueclaw can also be the agent. Started with `--acp-socket <path>`, it serves ACP on that unix socket (recreated at start, mode `0600`). `--inbound acp` makes ACP the only path that admits a message, and `POST /connectors/<platform>/events` then answers 409. See [ACP sessions](#acp-sessions).

# Concepts

## Connector

A connector is the adapter that turns one platform's message into the normalized event the runtime handles.

The platforms the protocol knows are `api` and `buzz` (`protocol/generated/json-schema/connector-platform.schema.json`). Every inbound event arrives at `POST /connectors/<platform>/events` with the same body:

```json
{
  "conversationID": "opaque-conversation-id",
  "messageID": "opaque-message-id",
  "senderID": "opaque-sender-id",
  "replyTargetID": "opaque-reply-target-id",
  "prompt": "current user message",
  "context": {
    "messages": [{ "speaker": "admin", "text": "previous visible message" }],
    "hasMoreBefore": true,
    "historyCursor": "opaque-history-cursor"
  }
}
```

The `api` connector identifies the sender by email and serves replies at `GET /agent/api/replies?conversationID=`. Messenger platforms go through `chatd`, the TypeScript bridge in `chatd/`, which holds the platform credentials, normalizes inbound messages and renders outbound replies. It ships adapters for Buzz and Mattermost. `connectors.chatd` configures it (`endpoint`, default `http://127.0.0.1:18090`; `timeoutSecond`; `enabledPlatforms`), and a platform listed in `enabledPlatforms` beyond the protocol's own is registered with a warning.

### Durability

Inbound events persist in `raw_event` with a `pending`, `running`, `succeeded` or `failed` status, and replies enqueue into `connector_outbox`. Background workers claim stale rows with retry and backoff, and a duplicate inbound event returns the stored result instead of running again.

### Control commands

An exact `/stop` or `/stop-all` (trimmed, lowercase) cancels the sender's task or tasks and gets a fixed acknowledgement (`internal/connectors/task_control.go`). It is the only reply blueclaw composes without a model.

## Task run

A task run is the durable record of one unit of work, from intake to its final reply.

A run has one of nine statuses: `planned`, `running`, `waiting_user_input`, `waiting_approval`, `blocked`, `interrupted`, `completed`, `failed`, `cancelled` (declared in bluecollar's `agentcontract/task_run.go`). Every transition goes through `TransitionTaskRun`, which records a transition event.

Restarts are explicit. Runs orphaned by a crash are interrupted at boot, runs in flight are interrupted before a planned shutdown (`POST /admin/api/runtime/prepare-shutdown`), and interrupted runs are claimed for auto-resume exactly once. A stale-task sweeper and a retention job run under `internal/scheduler`.

Within a run the model works in steps. A step either calls a tool, speaks to the requester, or fails the task; a final reply closes the task and must cite the observations that prove the work happened (the completion gate in bluecollar).

## Event ledger

The event ledger is the append-only list of events that belong to one task run.

Every model call, tool call, approval, tool exposure decision and launch step lands in it, which makes it the place to reconstruct what happened. Event names follow a fixed grammar declared in bluecollar's `agentcontract/task_event_name.go` and generated into `protocol/` as `task-event-name`: for example `tool.<name>.requested`, `tool.<name>.result`, `approval.pending_call`, `approval.executed`, `agent.instructions_loaded` and `llm.call`.

`GET /admin/api/run/detail?taskRunID=<id>` returns a run with its ledger, and `GET /tasks/api/events` streams one person's events over SSE.

## Approval

An approval is a person's answer to a tool call the host held before it ran.

The gate belongs to the host. `approvalgate.Gate.TurnGate` is installed as the `ToolCallGate` on every harness's tool set, so every harness meets the same gate on the same calls. A call is held when its descriptor sets `RequiresApproval`, or when the tool's input schema declares an `approvalRequired` field and the call sets it. An `external_send` into the conversation it was asked in (`targetType` `currentThread` or `currentChannel`) proceeds without asking. A delegated turn cannot ask and is denied.

A held call pauses the run in `waiting_approval` and records `approval.pending_call` with the exact call, so the approval survives a restart and blocks no live request. The question the person sees is written by the model. Once approved, the host carries out the recorded call verbatim in the `carryOutApprovedCall` launch step (`internal/agentruntime/approved_call.go`) and hands the result to the harness as `CarriedOutCalls`. A changed call is a new approval.

A grant can cover the rest of the task when the tool declares an `ApprovalScope`.

## Policy

The policy document is the list of people and circles blueclaw serves, and the source every identity is derived from.

It is the file passed as `--policy`. People carry a `personID`, `emails`, a security level (`securityLevelName`, `securityLevelRank`), `grantedClasses`, `circles` and `isAdmin`. Circles carry a `circleID`, a `workspaceDirectoryPath`, and optional `memberCircles` naming circles that belong to them. `resourceAccess` maps resources to actions and circles. The validator (`internal/policy/policy_validator.go`) checks IDs, emails, duplicates, circles (at most 10 per person), channels, resources and retention.

At boot the daemon projects the policy into a read-only `person` table and into POSIX users and groups (see [POSIX identity](#posix-identity)). The admin API validates, saves and reloads it (`/admin/api/policy/*`) and invites or removes people (`/admin/api/people/*`).

## Persona

The persona is three JSON documents that say who the agent is, how it carries itself, and how one person wants to be worked with.

| Document | Where | Carries | Read |
|---|---|---|---|
| `identity.json` | workspace root | the names the agent answers to (the first is how it introduces itself), an optional handle, a role, an emoji, an introduction | once, into the standing instructions and the turn briefing |
| `soul.json` | workspace root | what it holds to, what it never does, how it works, a tone, a language policy | once, into the standing instructions |
| `private/people/<personID>/.internkim/user.json` | the person's home, owned by their POSIX user | what they want to be called, what they want known, preferences, tone, language, `morningBriefing` | at every launch, read as that person through the helper |

Each is validated against a JSON Schema in `internal/persona/schema/` with `additionalProperties: false`, item and length ceilings, and a pinned `schemaVersion`. A document the schema refuses is restored from its backup in `.blueclaw/state/persona-backup`.

The agent edits only the requester's `user.json`, through `persona_read` and `persona_update`. The admin endpoints `/admin/api/persona/user` and `/admin/api/persona/agent` require a signed assertion in `X-Blueclaw-Memory-Assertion`: an HMAC-SHA256, keyed by the file at `memory.adminAssertionKeyPath`, over the method, the full request URI and a payload holding the reader, an expiry at most 60 seconds ahead and the body's SHA-256. An unsigned or invalid request is denied. Seeding the agent documents never overwrites an existing file, and an agent update cannot change the soul.

## Tool

A tool is a descriptor bound to a handler, and every behavior the runtime applies to a call is read from the descriptor.

A `toolcontract.ToolDescriptor` declares its namespace and policy resource, a `SideEffectClass`, whether it needs approval and what a grant covers, whether it needs the requester present or their own device, its visibility, its idempotency, whether its result can serve as completion evidence, and a result contract. Nothing dispatches on a tool's name.

The side effect classes are `none`, `read`, `computation`, `state_change`, `workspace_write`, `external_write`, `approval`, `connect`, `destructive`, `external_send`, `external_publish`, `local_file`, `platform_reply` and `site_publish`.

### The catalog

The kernel tools are `read`, `write`, `edit`, `bash`, `file_read`, `file_preview`, `file_delete`, `file_deliver`, `plan`, `equip`, `skill_search`, `skill_add`, `skill_remove`, `ask_input`, `conversation_history`, `memory_search`, `memory_remember`, `memory_forget`, `persona_read` and `persona_update`. Capability tools are added from the capability service's catalog at runtime. `docs/tool-catalog.md` is generated from the registering code with what each description costs.

At most 15 extension tools are offered to the model at once (`toolcontract.MaxExtensionCallableToolCount`); kernel tools come on top. What does not fit is reported as a dropped group in the exposure event.

### Registration

Providers register through `toolcontract.RegisterProviders`. A trusted provider that fails to load fails registration; an external provider that fails, or whose names collide with a registered tool, is quarantined and reported. A descriptor missing a required field, a model-visible tool without a result contract, or an object schema that does not set `additionalProperties: false` is rejected.

Keep input schemas shallow and portable across model providers: string-only enums, no `$ref`, no exotic `format`, with enumerated numbers stated in the description and checked by the runtime. The runtime does not enforce all of this; the harness rewrites `integer` to `number` and fills empty `properties`, and nothing rejects `const`.

## Skill

A skill is a directory with a `SKILL.md` that teaches the agent a workflow, loaded only when a task needs it.

Skills come from two roots: `<workspace>/.agents/skills`, which people manage with `skill_add` and `skill_remove`, and a bundled root at `/workspace/skills` (overridden by `BLUECLAW_BUNDLED_SKILLS_PATH`) that cannot be written or shadowed. The frontmatter keys read are `name`, `description`, `metadata.kim.intern.tool-references`, `kim.intern.requires-environment` and `kim.intern.requires-any-file`. `allowed-tools` is ignored: tool descriptors own policy, and only `tool-references` makes a skill depend on tools. The text `<skill>` in a body is replaced with the skill's install directory.

A skill whose required environment variables, tools or files are missing is set aside and logged as `skill.environment.missing`, `skill.tools.missing` or `skill.file.missing`.

### Progressive disclosure

The prompt carries a compact index of candidate skills, retrieved by BM25 and cached at `.blueclaw/skill-index.json`. The full `SKILL.md` body is injected only for skills selected for the turn whose tools are callable, at most five. The selection is recorded in `agent.instructions_loaded` with each skill's decision and reason and each source's path, size and SHA-256.

Every `SKILL.md` body a turn selects is charged on every step of that turn. Keep a normal skill under 8 KB and a complex artifact skill under 12 KB, and put long references, scripts and assets in `references/`, `scripts/` and `assets/` beside it to be read or run when the task needs them. blueclaw does not enforce a size limit on these files.

## Memory

Memory is what the agent keeps about the people it serves between tasks, stored by [bluememo](https://bluememo.intern.kim).

blueclaw embeds bluememo as a Go module pinned at `.dependency/bluememo`, on the Postgres API that version provides. Its schema is copied verbatim into `migrations/033_memory_store.sql`. A fact is one sentence a low-tier model extracts from an episode (a finished task run, or something a person asked the agent to remember), owned by that person and labelled with the security rank and classes of the conversation it came from. A fact with no circles is its owner's alone; a fact that names circles is also read by their members, and `memberCircles` nests circles. Facts are retired by supersession, forgetting or expiry.

Around the store, `internal/memory` does the following:

- Finished, failed and cancelled runs are queued for extraction unless `memory.extractionDisabled` is set.
- Launch loads the requester's profile and a recall of the prompt under character budgets and records `memory.recall_injected`.
- `memory_remember` stores one sentence and reports what it created, superseded or reinforced; `memory_forget` accepts only fact IDs that `memory_search` returned in the same task.
- Embeddings go through the capability service at `memory.embeddingModel` with 1,024 dimensions, and a model change is a `reembed` job. Profile and rehearse jobs run on the same worker.

bluememo has since moved to one SQLite file per person. The blueclaw integration will follow; until then it runs on the pinned Postgres version.

## Capabilities

A capability is an operation a separate service performs on the agent's behalf, such as sending a message, changing a calendar, or running a model.

blueclaw stays provider-neutral. It asks for a capability and passes an `executionMode` (`device`, `companion`, `remote` or `auto`, default `auto`); the capability service decides where it runs. The one choice blueclaw makes itself is sending `companion` for a tool whose `privacyClass` is `user_browser`. Descriptors mark tools that need the requester present (`requiresUserPresence`), their own device (`requiresRequesterDevice`) or a companion browser; tools that need the user present are not registered for scheduled runs.

The `capabilities` block names the service: `endpoint`, `unixSocketPath`, or `vsockCID` and `vsockPort` for a guest, plus `timeoutSecond`. The request and response shapes are Zod contracts in `protocol/` (`capability-descriptor`, `capability-registry-response`, `tool-invoke-request`, `tool-invoke-response`). A deployment without the block reports `capabilityd: not_configured` in health and runs without capability tools, capability-routed models, or memory embeddings.

## Schedule

A schedule is a task that runs on its own at a set time.

Schedules live in the `schedule` table and are polled every `scheduler.taskSchedulePollIntervalSecond`. The admin API lists, creates, updates, cancels and deletes them (`/admin/api/schedule/*`).

### Morning briefing

The morning briefing is a managed daily schedule per person. `morningBriefing` in `user.json` defaults to `{"enabled": true, "time": "08:00"}`, in the company time zone, and changes only through `persona_update`. The generic schedule endpoints cannot edit it. A briefing run may call only `task_list`, `event_list`, `conversation_history`, `memory_search` and `persona_read`, is skipped when there is nothing on the task and event lists, and is delivered through the outbox under an occurrence key so it arrives once. After five failures the occurrence is retired and the next day's runs as usual.

## Learning

Learning is a background review that turns finished work into learned skills and soul revisions.

It is off until enabled in `/admin/api/agent-learning/settings`. The worker checks every minute and reviews only when no foreground task is active, at least five minutes after the last observation and an hour after the last review, at most three times per UTC day. A batch holds at most 20 experiences from a single audience and 48 KiB of evidence, so evidence never crosses between audiences.

The reviewer answers with a closed schema: `keep`, `create`, `revise`, `replace`, `retire` or `soul`, citing 1 to 20 evidence IDs, and an independent assessment turns an unsupported change into `keep`. At most 20 learned skills are active, each capped at 8 KiB and 300 lines. Soul revisions are appended to `.blueclaw/state/persona/soul-history.json`. Operators list, retire, restore and protect learned skills and read the soul history under `/admin/api/agent-learning/*`.

## ACP sessions

An ACP session is a conversation an ACP client holds with blueclaw acting as the agent.

`session/new` and `session/load` read `_meta["kim.intern/session"]`, which carries the requester (`email`, `personID`, `name`, `callingName`, `handle`) and the addressing (`platform`, `conversationID`, `conversationType`, `replyTargetID`, `isThread`, `responseLanguage`); a session without a requester or conversation is refused. Each `session/prompt` reads `_meta["kim.intern/message"]`, passes the same engagement gate the connectors use, and launches a task. Progress goes out as `agent_thought_chunk` and tool calls, the reply as `agent_message_chunk` with attachments as resource links.

An approval on an ACP turn first pauses the run and records the held call, then asks through `session/request_permission` with the options `approve_once`, `approve_task` (only when the tool has an approval scope) and `reject_once`. The tool call ID is the held call's ID: `held-` and the first eight bytes of the SHA-256 of the canonical call. A free-text answer goes through the extension method `_kim.intern/approvalReply`, which the turn router reads. After `session/load`, every run of that requester and conversation still waiting on approval is asked again under the same ID.

# Boundaries

## POSIX identity

The POSIX identity is the Linux user and groups a person's tool calls run as, derived from the policy.

| Policy object | Linux object |
|---|---|
| person | `bc_person_<shortID>` user with a primary group of the same name |
| circle | `bc_circle_<circleID>` group |
| everyone | `bc_shared` supplementary group |
| service internals | `blueclaw` user |

Names are lowercased, reduced to `[a-z0-9_-]` and capped at 31 characters, with a hash suffix when normalizing lost information, so two people cannot collide onto one account (`internal/security/posix_identity.go`). The `admin` circle is not projected to a group; admin authority is a policy concept.

UIDs and GIDs are allocated from 100000 upward through a persisted table, which also adopts existing `bc_` users and groups, so a person keeps the same numeric identity across restarts and file ownership does not drift. Resolving an identity fails closed: an unknown user or group is an error and never falls back to the daemon's own identity.

On macOS the helper creates identities through `dscl` and `dseditgroup` and reads accounts from Directory Service; projected people get `/usr/bin/false` as a shell and are hidden from the login window. On Linux it uses `useradd`, `groupadd` and `usermod`.

## Workspace layout

The workspace is the directory tree under `terminal.workspaceRootPath` where people, circles and the service keep their files.

| Path | Owner:group | Mode |
|---|---|---|
| `private/people/<personID>` and its `artifacts/` and task temporary directory | the person | `0700` |
| `circles/<circleID>` | `blueclaw`:`bc_circle_<id>` | `2770` |
| `shared` | `blueclaw`:`bc_shared` | `2755` |
| `shared/public`, `shared/cache`, `shared/cache/dependencies/*` | `blueclaw`:`bc_shared` | `2775` |
| `private`, `private/people`, `circles` | `blueclaw`:`blueclaw` | `0711` |

`.blueclaw/` holds the daemon's state, logs, configuration and identity map. It appears in no projected entry, so it is never handed to a task user.

Use `shared/public` only for content that may be shown to anyone; share within the company through a circle directory. `shared/cache/dependencies` is for package caches alone: the `bun`, `go`, `pip`, `uv` and `cargo` caches point there, so it must never hold source or private files.

## Command execution

Command execution is how a tool call becomes a process running as the requester.

`CommandGuardrailService.BuildCommandPlan` (`internal/security/command_guardrail_service.go`) builds the plan and rewrites it to invoke the helper:

```text
blueclaw-posix-helper exec --uid <uid> --gid <gid> --groups <gids> --cwd <dir> -- <argv>
```

The helper is installed `root:root 4755` and accepts only a real UID of `root` or `blueclaw`. It calls `setgroups`, `setgid` and `setuid` in that order, then `exec`s with a canonical `PATH`, after which the process is the requester and cannot regain privilege. Its other commands are `capabilities`, `sync` (apply the projected users, groups and modes), `reconcile-home` and `fs` (one filesystem operation, after the same drop).

File tools are the same path. `read`, `write`, `edit`, `file_delete` and the rest build a shell command and run it through the requester's shell (`internal/agentruntime/requester_shell.go`), starting in the requester's `$HOME`, so tilde expansion, globs and relative paths behave as they would at that person's prompt.

### The environment

A command never inherits the daemon's environment. The child environment starts empty, takes `HOME`, `TERM` and `LANG`, copies only the names the workspace owns from what the call requested, and derives `HOME`, `TMPDIR` and the `XDG_*` paths from the resolved identity. A credential the daemon holds has no path into a task's shell, and there is no list of sensitive names to keep current. External agent CLIs are started the same way.

## What is not enforced

blueclaw has no executable allowlist, no denied command list, no denied path prefix, and no prompt instruction telling the model what it may not touch.

A command the requester may not run fails at the kernel, as it would for that person at a shell. Every such list would be a second copy of permissions the kernel already enforces, and it would go stale and block work the person is entitled to do. Inside the requester's permissions the agent may install a package, walk the filesystem or run a build without asking.

`TerminalConfiguration` has nine fields: `mode`, the sandbox provider, `workspaceRootPath`, `posixHelperPath`, `timeoutSecond`, `outputMaxBytes`, `sessionMaxCount`, `allowNetwork` and `allowInteractiveShell`. None is a list. The guardrail's checks are structural:

| Check | Where |
|---|---|
| refuses to execute when the daemon is effectively root | `BuildCommandPlan` |
| resolves the working directory against the workspace root | `resolveWorkingDirectoryPath` |
| builds the environment from the identity and forces a canonical `PATH` | `sanitizeEnvironmentVariables`, `applyPOSIXEnvironment` |
| caps the timeout | `timeoutSecond` |
| requires bubblewrap when `terminal.mode` is `sandbox` | `BuildCommandPlan` |

Two things in this path look like string filters and decide nothing: shell argument quoting is serialization, and matching stderr classifies a command that has already failed into a diagnostic code.

Effects that leave the machine, such as sending a message or publishing a site, are judged by a person at the [approval](#approval) gate.

## Capability authorization

Capability authorization is the per-person check on which capability operations a requester may invoke.

POSIX decides what a process may touch on this machine. It cannot decide whether a person may send a message as the company or change a shared calendar, because those effects happen in the capability service with that service's authority. `internal/access` (`CanAccess`) is consulted before capability and record catalog tools are exposed, and it is the only per-person authorization on those operations. It sits in front of the service's socket; moving the decision into the service that performs the effect is open work.

## Known gaps

These are the places where the boundary above does not hold as stated.

- With `terminal.posixHelperPath` empty there is no projection. Requester tools fail closed, and any command the guardrail still runs runs as the daemon user.
- The virtual-session scripted harness (`internal/e2e/virtual_session.go`) uses `DirectWorkspaceActorFactory`, which has no projection, because one scenario in one workspace has no second person to isolate. A deployment must never use it.
- The POSIX separation tests need root and an installed helper, so an ordinary `go test ./...` skips them. They run on Linux and macOS when `BLUECLAW_TEST_POSIX_HELPER` or `BLUECLAW_TEST_POSIX_HELPER_PATH` is set (`tests/integration/`).
- The admin API (`/admin/api/*`) has no session authentication of its own; it rejects cross-origin mutating requests, and the persona, schedule-tool and learning endpoints require a signed assertion. Keep the listen address on loopback or behind something that authenticates.

## Model-written wording

Model-written wording is the rule that every sentence a person reads from the agent comes from a model.

Replies, approval questions, failure explanations and recovery direction go through the model. Deterministic code validates, normalizes, enforces schemas, orchestrates retries and records diagnostics, and it may hand the model safe facts (failure stage, error code, attempted actions), but it does not compose sentences for people. When a failure needs a judgment, the runtime asks for structured output first and feeds that decision into the reply.

A real task failure always produces a reply. The failure notice is tried in order: written by the task's model, possibly rewritten by a review step, written by the local or device model, and last a raw error with secrets redacted, compacted to 600 characters (bluecollar's `agentcontract/failure_notice.go`). Replies are suppressed only for duplicates, cancelled output and messages from the bot itself. The exception is the exact `/stop` and `/stop-all` acknowledgement.

# Operations

## Runtime configuration

The runtime configuration is the JSON file passed as `--runtime`, and it holds every setting except the people.

| Block | Holds |
|---|---|
| `baseURL` | the address the daemon listens on and advertises |
| `languageModel` | model tiers, embedding model, tier bounds, context window; see [Language models](#language-models) |
| `database` | `driver`, `connectionString`, `migrationDirectoryPath`, `maxOpenConnections` |
| `memory` | `embeddingModel`, `embeddingExecutionMode`, `extractionDisabled`, `adminAssertionKeyPath` |
| `agent` | `intake`, `defaultTaskLevel`, `failureRecovery`, `harness`, `optionalFileReadPathSuffixes` |
| `agentProfiles` | named profiles with `allowedToolNames` |
| `capabilities` | the capability service; see [Capabilities](#capabilities) |
| `connectors` | `chatd` |
| `terminal` | the execution settings in [What is not enforced](#what-is-not-enforced) |
| `scheduler` | `retentionCheckIntervalMinute`, `taskSchedulePollIntervalSecond` |
| `logging` | `directoryPath`, `retentionDays` |
| `guest`, `bridge` | the virtual machine guest and the companion bridge |

`agent.defaultTaskLevel` is the effort a task starts at, `xlow` through `max`. `agent.harness` takes `name`, `agentCommandPath`, `agentArguments` and `toolCatalogURL`. `optionalFileReadPathSuffixes` names files whose absence is a normal state for a deployment.

`config/runtime.standalone.example.json` is a single-process shape and `config/runtime.example.json` a guest-and-capability-service shape.

## Language models

The language model configuration says where each effort tier reaches a model; blueclaw decides nothing about which model that is.

There are six tiers, `xlow`, `low`, `medium`, `high`, `xhigh` and `max`, and `maximumModelTier` and `minimumModelTier` bound where the runtime may move a task. Two shapes exist, and a configuration that names both is refused (`internal/llm/provider_factory.go`):

- **`tiers`** maps each tier to an ordered list of endpoints. Each entry has `endpoint`, `model`, and optionally one key source, `apiKeyEnvironment` (the name of an environment variable) or `apiKeyPath` (a file), then `reasoningEffort`, `providerOrder` and `providerSort`. The `Authorization` header is sent only when a key source is named. Entries are tried in order, so a deployment writes its own fallbacks. A tier with no entry is an error.
- **`capability`** names a model per tier (`xlowModel` … `maxModel`), a `decisionModel` for intake, and an `executionMode`, and hands model choice, local runtimes and fallback to the capability service. No key appears in the file.

`embedding` and `decision` are single entries of the same shape. Each is reached at its endpoint when it names one, and through the capability service otherwise. `decision` speaks the decisions API that Jev and Kev serve.

Any string in the runtime file may contain `${NAME}`, filled in from the environment at load time; an unset or empty variable is refused by name. A `$` without braces is left as written.

Every structured call leaves as a single function tool with `tool_choice` forcing it, and the runtime reads the call's arguments; it never sends `response_format`. Some local servers treat a forced choice as a hint, so a small model may answer in prose and fail the turn.

## Database

The database is one Postgres instance holding the task store, the ledger, connector queues, schedules and memory.

Migrations are the numbered files in `migrations/`, embedded in the binary and applied in order at boot, forward only. A schema change is a new file; never edit an applied one.

With `database.maxOpenConnections` unset or 0, the daemon asks Postgres for `max_connections` minus `superuser_reserved_connections` and fails if that is below 1. Idle connections match the open limit, idle time is 10 minutes and a connection's lifetime 30 minutes. Health reports pool exhaustion (in use, allowed, waiters) when it cannot get a connection.

## HTTP surface

The HTTP surface is every route the daemon serves, defined in `internal/httpserver/router.go`.

| Prefix | For |
|---|---|
| `/admin/api/*` | operators: health, policy and people, runs, approvals, harness, skills, tools, learning, memory facts, schedules, workspace files, persona, backup, quiesce |
| `/tasks/api/*` | one person's own runs (list, detail, cancel, SSE events), addressed by a `taskSessionID` from a magic link |
| `/agent/api/replies` | reply polling for the `api` connector |
| `/connectors/<platform>/events` | connector ingress (absent under `--inbound acp`) |
| `/harness/tool-catalog` | the per-turn MCP catalog for external harnesses |
| `/debug/pprof/` | Go profiling |
| `/admin`, `/tasks`, `/login`, `/_app` | the Svelte console, built from `admin/` into `web/admin` |

`GET /admin/api/health` reports `status`, `languageModel`, `database` (with the connection pool), `connector`, `backlog`, `protocolIdentity` and `failureReasons`. `protocolIdentity` fails when the Go types and the generated JSON Schemas in `protocol/` have drifted, or when the capability service reports a different protocol; an unconfigured service reports `not_configured` and passes.

## Deployment

A deployment is the daemon, Postgres, the setuid helper and, optionally, a capability service and `chatd` beside it.

The simplest shape is `cmd/blueclaw` as an ordinary process. A stronger shape runs it inside a virtual machine guest under `cmd/blueclaw-supervisor`, which boots the guest under Cloud Hypervisor or vfkit, mounts the workspace, proxies host and guest HTTP over vsock, and restores the workspace image. Cloud Hypervisor disks are attached as `image_type=raw` with PCI left on, and the delivery directory is served over virtio-fs.

| Binary | Purpose |
|---|---|
| `cmd/blueclaw` | the daemon |
| `cmd/blueclaw-posix-helper` | setuid identity switch, POSIX state sync, filesystem operations |
| `cmd/blueclaw-cli` | terminal client and enrollment |
| `cmd/blueclaw-supervisor` | boots and watches the guest |
| `cmd/blueclaw-guest-healthd`, `cmd/blueclaw-vsock-http-proxy` | guest health and host-to-guest transport |
| `cmd/blueclaw-backup`, `cmd/blueclaw-restore` | workspace and database snapshot bundles |
| `cmd/blueclaw-lab` | development VM lifecycle and scenario runner |

### Which revision is running

`blueclaw --version` prints it and `GET /admin/api/harness` carries it. The value comes from `-ldflags`, because blueclaw is often built as a submodule whose `.git` points outside the tree:

```bash
go build -ldflags "-X github.com/yeomyeonggeori/blueclaw/internal/buildrevision.injected=$(git rev-parse HEAD)" ./cmd/blueclaw
```

A build without it reports `unknown`, and a deploy check should refuse that.

### Restarting safely

A running process keeps the configuration it started with. Before replacing the binary, call `POST /admin/api/runtime/prepare-shutdown` so runs in flight are interrupted and resumed once afterwards, or `POST /admin/api/quiesce` to stop taking new work. `/admin/api/backup/prepare` and `/complete` bracket a snapshot.

## Development

Development means building and testing the Go daemon and the three TypeScript packages.

```bash
go build ./...
go vet ./...
go test ./...
bun install
bun run test
```

`protocol`, `chatd` and `admin` are one Bun workspace. `bun run test` at the root typechecks and runs each package in its own process; do not run bare `bun test` at the root. CI runs these commands with Postgres 16 as a service. After changing a contract in `protocol/src`, run `bun run generate` there; `bun run generate:check` rejects stale artifacts. Zod is the canonical contract, and Go types validate against the generated schemas. A breaking contract change bumps the protocol version. The package defines the shape of a capability descriptor and no tools; a product offers its own catalog to the scenarios through `BLUECLAW_SCENARIO_CAPABILITY_CATALOG`.

| Tier | Gate |
|---|---|
| Unit | none |
| Postgres-backed | `BLUECLAW_TEST_POSTGRES_URL` |
| Live model (costs money) | `BLUECLAW_LIVE_LLM_TEST=1` |
| External agent | `BLUECLAW_TEST_CLAUDE_CODE_PATH`, `BLUECLAW_TEST_CODEX_PATH`, `BLUECLAW_TEST_ANTIGRAVITY_PATH`, `BLUECLAW_TEST_ACP_AGENT_PATH` |
| POSIX separation | `BLUECLAW_TEST_POSIX_HELPER`, `BLUECLAW_TEST_POSIX_HELPER_PATH` (root and an installed helper) |
| Virtual session | `--live-llm` or `BLUECLAW_E2E_LIVE=1` |

A virtual session drives the agent loop with no VM and writes every request, response, tool call and artifact to a directory:

```bash
go run ./cmd/blueclaw-lab virtual-session --scenario presentation \
  --artifact-dir .artifacts/blueclaw-e2e --live-llm
```

Scenarios are defined in `internal/e2e/scenarios.go`; `--scenario-file` loads one from JSON.

### The lab

`cmd/blueclaw-lab` also drives a full rig: an Apple Silicon Mac as the person's computer, a Tart ARM Linux VM, and blueclaw inside a guest that VM boots under Cloud Hypervisor. `config/lab.example.json` configures it and `lab/scripts/` holds provisioning and scenario scripts. The commands are `image-build`, `vm-up`, `vm-down`, `vm-ssh`, `scenario-mattermost`, `scenario-slack` and `scenario-browser-handoff`. Tart and a hardware-virtualized guest cannot start on a hosted CI runner, so the lab is not exercised by CI.

### Screenshots

The terminal client images in the README are generated by `./tools/shoot-tui-screenshots`, which builds the client, serves a seeded admin API and renders each screen with headless Chrome. Re-shoot them after changing `internal/tui`.

`AGENTS.md` holds the conventions the code follows.

# Q&A

### Can I run Claude Code or Codex inside blueclaw?

Yes. Set `agent.harness.name` to `claude-code`, `codex`, `antigravity` or `acp`, and point `agentCommandPath` at the executable. The agent starts as the requester's POSIX user and reaches blueclaw's tools through an MCP catalog published for that turn. blueclaw refuses to start it unless the POSIX boundary is configured, because the agent brings a shell of its own.

### How is this different from running the agent in a container?

A container isolates the agent from the machine. blueclaw isolates the people using the agent from each other inside one workspace: different Linux users, different `0700` homes, different groups. The two compose, and `terminal.mode` `sandbox` adds bubblewrap where it is available.

### What stops the agent from running a dangerous command?

The requester's own permissions. The command runs as an unprivileged user and the kernel refuses what that user may not do. Calls with effects beyond the machine go to a person at the approval gate before they run.

### Does it need Linux?

No. It is deployed on Linux, and macOS runs the same POSIX separation tests. macOS has no bubblewrap, so `terminal.mode` stays `native` there.

### Do I need a chat platform?

No. The `api` connector accepts a JSON POST and serves replies over HTTP. Postgres is required: the task store and the ledger live there.

### Can a harness call tools in parallel?

The bundled loop uses native tool calling, and several calls in one model response run as a batch in order, stopping at the first failure. A call whose input depends on an earlier result still costs a round trip; letting the model write code that calls tools would remove that, but only with a new privileged channel from the requester's process into the runtime, which the POSIX model avoids.

### Is there a binary release or a Docker image?

No. Build from source.

### Where do I report a security problem?

Follow [SECURITY.md](https://github.com/yeomyeonggeori/blueclaw/blob/main/SECURITY.md) and do not open a public issue.
