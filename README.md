<img src="assets/blueclaw.logo.svg" alt="blueclaw" width="112">

# blueclaw — a POSIX-isolated, multi-user agent host

[![CI](https://github.com/yeomyeonggeori/blueclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/yeomyeonggeori/blueclaw/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

> **Status: pre-alpha, under active development.** The interfaces, the wire
> grammar, the configuration keys and the database schema all still change
> without notice, and there is no release, no versioning policy and no upgrade
> path between commits. It is published so the design can be read and argued
> with, not so it can be depended on. If you run it, pin a commit and expect to
> read diffs.

A company runs one agent on one machine and everyone talks to it. To a harness
that runs as whoever started it, the whole company is one Unix account: one home
directory, one set of files, one view of every secret, and no record of which
person authorized which side effect. Sales can read engineering's drafts because
nothing on the machine knows they are different people.

blueclaw is the host that makes them different people, and POSIX is how. It is
an open source, self-hosted Go daemon that runs an AI agent harness on behalf of
whichever person asked, executes each requester's tool calls as their own
unprivileged Linux user, holds side-effecting calls at an approval gate, and
writes every step to a durable event ledger. It owns identity, isolation, task
state, the tool catalog, and delivery.

<img src="assets/screenshots/tui-tasks.png" alt="blueclaw showing five task runs, one waiting for approval" width="100%">

`blueclaw-cli` is the terminal client above; the host is what it connects to.

The agent loop is a replaceable component behind a Go interface
(`agentcontract.Harness`, one method). Five harnesses are selectable, one of them in this
repository. Swapping it does not move the isolation boundary, because tool
execution never leaves blueclaw.

## What blueclaw is, and what it is not

| It is | It is not |
|---|---|
| a host process that runs an agent harness on other people's behalf | an agent, an agent loop, or a model |
| a POSIX identity boundary between the people sharing one machine | a container runtime or a sandbox technology |
| a durable task store with an append-only event ledger | a chat client |
| an MCP client that mounts external tool servers into the catalog, and an MCP server that publishes the requester's catalog to an external harness | a general-purpose MCP server for arbitrary clients |
| a Go binary you build from source and self-host | a hosted service or a packaged binary release |

Read the right-hand column literally. blueclaw separates requesters from each
other, not the agent from the machine; a container does the latter, and the two
compose.

blueclaw is the agent host inside InternKim, an on-premise AI automation
appliance.

## Why an agent host, not another agent

Agent harnesses are already abundant: Claude Code, Codex, opencode, Gemini CLI,
and the loop bundled here. They all run as whoever started them. On a shared,
multi-user machine that means one Unix account for every requester, no
per-person file separation, and no record of which human authorized which side
effect.

Self-hosted assistants have the same shape from the other direction.
[openclaw](https://github.com/openclaw/openclaw) and the projects around it put
a capable agent on your own machine, and that is the right answer for one
person. Nothing stops a team pointing several people at one of them. They were
simply not designed for it, so the second person who asks for something gets the
first person's home directory, the first person's secrets, and a ledger that
cannot say who authorized what. blueclaw starts from the assumption those
projects do not make.

blueclaw takes the opposite split. The harness decides *what* to call. blueclaw
decides *who it runs as*, *whether it runs at all*, and *what is written down*.
A harness that executes tools inside its own process is not an acceptable
integration, because it takes back the only thing the host exists to provide.

The mechanical claim is narrow and testable: tool execution runs as an
unprivileged POSIX user derived from each requester's identity, and
[POSIX](https://pubs.opengroup.org/onlinepubs/9799919799/) ownership and mode
bits are the only access boundary. There is no executable allowlist, no denied
command list, no denied path prefix, and no instruction anywhere in a prompt
telling the model what it may not touch. A command the requester may not run is
not refused by blueclaw; it fails at the kernel, the same way it would fail for
that person at a shell.

That is an opinion, and it cuts both ways. Inside the requester's permissions
the agent is left alone — it may install a package, walk the filesystem, run a
build, try something and undo it, without asking a policy engine whether each
step is on a list. Every such list is a second, worse copy of the permissions
the kernel already enforces: it goes stale, it blocks work the person is
entitled to do, and the model learns to route around it. Confining a process is
a solved problem, and the solution is fifty years old.

So the boundary is drawn once, at identity, and it is absolute. What is left is
judgment about effects that leave the machine — sending a message, publishing a
site, changing a shared calendar — and that goes to a person at the approval
gate.

## How it works

- **connector** — a platform adapter; normalizes an inbound message.
- **task run** — a durable record of one unit of work.
- **event ledger** — the append-only events belonging to a task run.
- **approval gate** — holds a tool call until a human authorizes it.
- **POSIX projection** — maps policy objects to real Linux users and groups.

```text
  chat platform / HTTP ingress
            |
            v
  blueclaw (Go daemon)
    connectors · policy · task store · approvals · tool catalog · POSIX projection
            |
            +-- agentcontract.Harness --+-- .dependency/bluecollar   (Go, in-process)
            |                           +-- an ACP or CLI agent, started as the requester
            |
            +-- tool execution --> blueclaw-posix-helper --> each requester's UID/GID/groups
```

### Intake: connectors normalize a message

Four connector adapters are registered at boot — `mattermost`, `slack`,
`signal`, and `api` — plus `buzz` when `connectors.buzz.enabled` is set
(`internal/app/application.go`). Each turns a
platform-specific payload into the same normalized conversation turn. The `api`
connector needs no chat platform at all and is the way to drive blueclaw from
`curl` or a test.

Intake resolves the sender to a person in the policy document. That resolved
person, not the daemon account, is the identity every later step runs under.

### Task runs and the event ledger

A task run is a row in the task store with one of nine statuses
(`.dependency/bluecollar/taskstate/task_type.go`): `planned`, `running`, `waiting_user_input`,
`waiting_approval`, `blocked`, `interrupted`, `completed`, `failed`,
`cancelled`.

Every step appends an event through `TaskEventService.AppendTaskEvent`
(`.dependency/bluecollar/taskstate/task_event_service.go`). Event names follow a fixed wire grammar —
`tool.<name>.requested`, `tool.<name>.result`, `approval.pending_call`,
`approval.executed`, `agent.task_launched`, and, for a tool an external harness
ran outside the catalog, `harness.tool_permitted` and `harness.tool_refused`. A
reader can reconstruct what happened without access to the harness's internal
types.

### Approval gates

An approval gate pauses a task run before a side-effecting tool call executes
and records `approval.pending_call` with the exact call
(`internal/approvalgate`, `taskstate.TaskRunService.PauseTaskRun`). Because the
held call is persisted, approval survives a daemon restart and does not block a
live request.

How the approved call then runs depends on which harness held it. The bundled
loop replays the recorded call verbatim
(`.dependency/bluecollar/approval_gate.go`). Every other harness is told in its
next prompt that the approval arrived and asked to issue that exact call again,
so the replay depends on the agent reproducing its own arguments. Levelling
every harness up to a verbatim replay needs the result of a host-executed call
to reach the agent, and the only channel for that today is the loop's own
in-process observation format, so it waits for the ledger to cross the harness
boundary.

Which calls are gated comes from descriptor metadata, not from the tool's name.
`toolcontract` carries an `ApprovalScope` and one of 14 `SideEffectClass` values
per tool (`.dependency/bluecollar/toolcontract/registry.go`), from `none` and `read` through
`workspace_write`, `external_send`, `site_publish`, and `destructive`.

The gate belongs to the host, not to a harness. `internal/approvalgate` holds
the call and `internal/mcpserver` consults it before invoking anything from the
catalog, so every harness meets the same gate on the same calls.

Sending a message,
changing a calendar, publishing a site — every outward or irreversible effect
exists only as a catalog tool, and an external agent has no other route to one.
`internal/acpharness` also refuses ACP's own filesystem and terminal methods,
which pushes an agent's file and shell work back onto the catalog rather than
letting it run beside the gate.

What remains outside is the tools an agent runs inside its own process: goose's
shell, Claude Code's editor. Those are answered yes. The boundary there is
POSIX — the agent runs as the requester's unprivileged user, and a shell call it
makes itself can do no more than one made through `shell`. The answer is
recorded: `harness.tool_permitted` and `harness.tool_refused`
go to the event ledger, so reading a task afterwards shows the calls the catalog
never saw.

One asymmetry is not ours to fix. ACP has a permission channel and a command
line does not, so a CLI harness offers nothing to record.

### POSIX identity projection

Every person in the policy document projects to a real Linux user and every
circle to a real group (`internal/security/posix_identity.go`):

| Policy object | Linux object | Symbol |
|---|---|---|
| person | `bc_person_<shortID>` user with a primary group of the same name | `LinuxPersonUserName` |
| circle | `bc_circle_<circleID>` group | `LinuxCircleGroupName` |
| everyone | `bc_shared` supplementary group | `posixSharedGroupName` |
| service internals | `blueclaw` user | `blueclawServiceUserName` |

Names are lowercased, reduced to `[a-z0-9_-]`, and capped at 31 characters. A
lossy or truncated normalization gets a hash suffix, so two people cannot
collide onto one account (`shortenedLinuxName`).

`POSIXStateForPolicy` compiles the policy into the users, groups, and directory
modes the daemon applies at every boot:

| Path | Owner:group | Mode |
|---|---|---|
| `<workspace>/private/people/<personID>` (and `tmp/`, `artifacts/`) | the person | `0700` |
| `<workspace>/circles/<circleID>` | `blueclaw`:`bc_circle_<id>` | `2770` |
| `<workspace>/shared` | `blueclaw`:`bc_shared` | `2755` |
| `<workspace>/shared/public`, `<workspace>/shared/cache/**` | `blueclaw`:`bc_shared` | `2775` |
| `<workspace>/private`, `<workspace>/private/people`, `<workspace>/circles` | `blueclaw`:`blueclaw` | `0711` |

`<workspace>/.blueclaw` — the daemon's own state, logs, configuration, and
identity map — appears in no projected directory entry, so it is never chowned
or chgrped to a task user and stays owned by the service account.

UIDs and GIDs are allocated from 100000 upward through a persisted allocation
table (`cmd/blueclaw-posix-helper/main.go`), so a person keeps the same numeric
identity across restarts and reprovisions and existing file ownership does not
drift.

## Quickstart

There is no packaged install path in this repository: no Makefile, no
Dockerfile, no service unit, no release binaries. Running blueclaw means
building from source. The appliance tooling that provisions, packages, and
deploys it lives in a separate private repository.

Requirements: Go 1.26, [Bun](https://bun.sh) 1.3, Postgres, and one
OpenAI-compatible model endpoint — Ollama, vLLM, LM Studio, OpenRouter, or
anything else speaking that API.

**1. Point the daemon at a model.** Copy
`config/runtime.standalone.example.json` and fill in `languageModel.direct`: the
base URL of an OpenAI-compatible server and the model name.
`http://127.0.0.1:11434/v1` is Ollama. A hosted provider such as OpenRouter
takes an `apiKeyPath` beside them, a file holding the key; a local server that
authenticates nobody leaves it out. That one model name is what every tier asks
for until `languageModel.capability` names tiers of its own.

Treat a local model as a development convenience. Every structured call leaves
as a single function tool with `tool_choice` forcing it, and the runtime reads
that tool call's arguments as the answer; it never sends `response_format`.
Ollama treats the forced choice as a hint, so a model may answer in prose, and
the turn fails with "answered stop with prose instead of calling the schema it
was given". Small models also struggle with the larger runtime schemas.

**2. Start the daemon.** Set your Postgres connection string in the same file,
then:

```bash
go run ./cmd/blueclaw --runtime runtime.json --policy config/policy.example.json
curl -s localhost:8081/admin/api/health | jq '.status, .protocolIdentity.passed'
```

`cmd/blueclaw` takes exactly two flags, `--runtime` and `--policy`; everything
else is configuration. The 29 migrations under `migrations/` are applied in
order at boot.

A standalone deployment reports `capabilityd: not_configured`. There is no
capability service, so the calendar, task, mail, and site operations an
appliance supplies are simply absent. The agent loop, skills, the
terminal, and files work.

**3. Enable per-person POSIX isolation.** Until this step every requester shares
the daemon's account; this is the step that makes blueclaw multi-user. The
projection is applied only when
`terminal.posixHelperPath` is set. Build and install the setuid helper, then
point the configuration at it:

```bash
go build -o /usr/local/bin/blueclaw-posix-helper ./cmd/blueclaw-posix-helper
sudo chown root:root /usr/local/bin/blueclaw-posix-helper
sudo chmod 4755 /usr/local/bin/blueclaw-posix-helper
```

The daemon then synchronizes users, groups, and directory modes from the policy
document at every boot (`internal/app/application.go`).

**4. Give it work, as two different people.** Address two people from your
policy by email through the `api` connector:

```bash
for sender in ada@example.com grace@example.com; do
  curl -s -X POST localhost:8081/connectors/api/events -H 'content-type: application/json' \
    -d "{\"conversationID\":\"dm:api:$sender\",\"messageID\":\"m1\",\"senderID\":\"$sender\",
         \"replyTargetID\":\"dm:api:$sender\",\"prompt\":\"Write your name to a file in your home directory.\"}"
done

curl -s 'localhost:8081/agent/api/replies?conversationID=dm:api:ada@example.com'
```

The two runs execute as different Linux users with different `0700` home
directories. Neither can read the other's file, and the ledger records which of
them authorized which side effect. That is the whole claim, and it takes about
thirty seconds to watch.

To watch a whole scenario instead, the lab runner drives the agent loop and
writes every request, response, tool call, and artifact to a directory:

```bash
go run ./cmd/blueclaw-lab virtual-session --scenario presentation \
  --artifact-dir .artifacts/blueclaw-e2e --live-llm
```

Live runs spend money, so they are never enabled by configuration alone. The
explicit `--live-llm` flag or `BLUECLAW_E2E_LIVE=1` is required
(`cmd/blueclaw-lab/main.go`). Scenario names resolve through
`e2e.BuiltinScenario`; the scenarios are defined in `internal/e2e/scenarios.go`,
and `--scenario-file` loads one from JSON instead.

## Terminal interface

`cmd/blueclaw-cli` is how you watch and answer a running daemon from a terminal.
It talks to the admin API over HTTP, so it runs wherever you can reach the
daemon.

```bash
go build -o blueclaw-cli ./cmd/blueclaw-cli
./blueclaw-cli --base-url http://127.0.0.1:8081
```

With no `--base-url` it reads the one recorded at enrollment. `--runtime`
points at the daemon's runtime configuration and is only needed when the
daemon is unreachable and you still want to see which harness is configured.

Four screens, switched with `1`-`4` or `tab`; `up`/`down` selects, `enter`
opens, `r` refreshes, `q` quits.

**Detail** replays one task run from its event ledger — every tool call, its
result, and the approval that is holding the run open.

<img src="assets/screenshots/tui-detail.png" alt="the detail screen replaying a task's tool calls and its pending approval" width="100%">

**Approvals** lists every run waiting on a person and shows the question the
daemon asked. `y` confirms the held call, `a` confirms every held call in that
task, `n` cancels. The answer is written to the ledger, so approving survives a
daemon restart; see Approvals for how the held call then runs.

<img src="assets/screenshots/tui-approvals.png" alt="the approvals screen showing a pending question with confirm and cancel keys" width="100%">

**Harness** reports which loop the running daemon is using and
whether tool calls execute as the requester's own POSIX user or as the daemon
account.

<img src="assets/screenshots/tui-harness.png" alt="the harness screen reporting bluecollar running as the requester's own POSIX user" width="100%">

## Harnesses

A *harness* is the agent loop: it runs a turn and reports what happened. In
blueclaw a harness is anything satisfying `agentcontract.Harness`
(`.dependency/bluecollar/agentcontract/harness.go`), a single method:

```go
type Harness interface {
	RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
}
```

The port was deliberately shrunk. It began at nine methods. Most of them
asked a model a question instead of asking an agent to work: classify whether a
chat message was addressed to the bot, write a reply sentence, refresh a skill
index, route a turn, complete a launch failure. No external harness could answer
those honestly, so nothing but blueclaw's own loop could implement the port.
Those are host policy now. `internal/reply` and `internal/agentruntime` hold
most of them; turn routing is still a package in the bluecollar repository
(`.dependency/bluecollar/intake`) that the host runs before every turn, because
`AgentTurnRequest.PrecomputedTurnDecision` is required and a harness that
arrives without one is refused. See Project status.

The host opens the task run. Every harness is handed an `ExistingTaskRunID` and
settles the run it was given, so a first turn is recorded the same way whichever
loop ran it.

A harness that only ever sees a prompt is briefed in it. The standing
instructions the host assembles, the agent's own name and handle, the company,
who is asking, the language to answer in, and the facts already recalled arrive
as a preamble ahead of the request (`internal/turnbriefing`). The bundled loop
takes the same material through its instruction bundle and turn request.

The ledger is not owned that cleanly yet. Of 126 event names, the host writes 42
from what it observes at its own boundaries, the bundled loop writes 73 from
inside the turn, and 11 have a writer on each side, so which one fires depends
on the configured harness. Six of the 11 are the approval family, where a second
gate lives in the loop; five are the failure family, split between
`internal/launchfailure` for a failure before the turn and the loop for one
during it. An external harness produces none of the loop's 73, which is why a
turn under one reads thinner than the same turn under bluecollar. Consolidating
that is the current work.

Everything else the host needs — task events, cancellation, run lookup — it
takes from the task store.

The harness is injected at the top. `main` passes a factory:

```go
application := app.NewApplication(runtimeConfiguration, *policyPath, bundledHarnessFactory())
```

[bluecollar](https://github.com/yeomyeonggeori/bluecollar) is the bundled
implementation. It lives in its own repository, pinned here as a submodule at
`.dependency/bluecollar`, and carries `agentcontract` with it because both sides
compile against it.

Building with `-tags nobundledharness` leaves the loop out of the binary, so
`agent.harness.name` has to name one of the others. `agentcontract` and `intake`
still compile in as contract and host routing; a test in `cmd/blueclaw` fails if
the loop creeps back.

The scenario rig binds the same factory production does. It used to have a
harness port of its own, because a scenario pins a turn budget and a runtime
configuration has no way to say "two iterations". That budget is a host
capability now, so `harnessdriver` has one `Dependencies` and one `Factory`, and
the rig reaches its harness through the door production uses.

`agent.harness.name` in the runtime configuration selects which harness runs:

| Name | What it runs |
|---|---|
| `bluecollar` (default) | the bundled in-process loop |
| `acp` | any [ACP](https://agentclientprotocol.com) agent named by `agentCommandPath` |
| `claude-code` | the `claude` CLI in headless mode |
| `codex` | the `codex` CLI |
| `antigravity` | the `agy` CLI |

The catalog reaches an ACP agent over whichever MCP transport it takes. Agents
that advertise `mcpCapabilities.http` are handed the endpoint and a session
token directly. Everyone else is handed a stdio server: the daemon binary in
`mcp-tool-catalog` mode, spawned by the agent under its own identity, proxying
to that same endpoint. The Agent Client Protocol requires every agent to support
stdio and leaves http optional, so stdio is the floor.

`internal/acpharness`, built on `coder/acp-go-sdk`, is blueclaw acting as an ACP
*client*: it starts the external agent inside the requester's POSIX identity,
publishes the requester's tool catalog over MCP, and lets the kernel — not a
deny list — decide what that agent may do. `internal/cliharness` does the same
for agents that speak a command line; each one is a descriptor of flags and an
output parser.

An external harness brings tools of its own, so both refuse to run at all
unless the requester's POSIX boundary is configured. Neither can report a task's outcome
the way the bundled loop does. A CLI agent that ends its turn has said its
piece, not finished the work, so blueclaw decides the outcome itself from the
agent's final message and the catalog tools that actually ran
(`internal/turnoutcome`).

## blueclaw versus Claude Code, Codex, opencode, and Gemini CLI

These are agent harnesses. blueclaw is the host they are intended to run inside.
The question is which layer owns what.

| Concern | Claude Code, Codex, opencode, Gemini CLI | blueclaw |
|---|---|---|
| Agent loop | yes, that is the product | delegated to a harness behind an interface |
| Model choice | yes | delegated to the configured provider, swappable mid-run |
| Runs as | the operating system user who started it | an unprivileged user derived from the requester |
| Multi-person separation | none; one process, one account | per-person Linux user, group, and `0700` home |
| Approval | in-process prompt, lost on exit | persisted `approval.pending_call`, carried out later |
| Audit record | terminal scrollback and local session files | append-only event ledger in Postgres, per task run |
| Inbound surface | a terminal | chat connectors and HTTP ingress |
| Work lifetime | one interactive session | durable task runs across restarts |

## Security model

Report security problems through [SECURITY.md](SECURITY.md), not a public issue.

### Applying the identity

`CommandGuardrailService.BuildCommandPlan`
(`internal/security/command_guardrail_service.go`) builds the plan;
`applyPOSIXRunner` rewrites it to invoke the setuid helper:

```text
blueclaw-posix-helper exec --uid <uid> --gid <gid> --groups <gids> --cwd <dir> -- <argv>
```

The helper (`cmd/blueclaw-posix-helper/main.go`) is installed `root:root 4755`.
It authorizes only a real UID of `root` or `blueclaw`
(`authorizeHelperCaller`, `isAuthorizedHelperCaller`), then calls `setgroups`,
`setgid`, and `setuid` in that order (`applyIdentity`) before `syscall.Exec`.
After that call the process is the requester and cannot regain privilege.

File tools are not a separate code path. `read`, `file_write`, `file_edit`
and the rest build a shell command and run it through the same requester
primitive (`internal/agentruntime/requester_shell.go`), starting in the
requester's own `$HOME` (`requesterShellScript`), so tilde expansion, globs, and
relative paths carry native POSIX semantics instead of a hand-written path
vocabulary.

### The environment a command runs with

A command never inherits the daemon's environment. `sanitizeEnvironmentVariables`
(`internal/security/command_guardrail_service.go`) starts from an empty map, sets
`HOME`, `TERM` and `LANG`, then copies allowed names out of the environment the
tool call itself requested. `os.Environ()` is not one of its inputs.
`applyPOSIXEnvironment` (`internal/security/posix_identity.go`) derives the rest
from the resolved identity: `HOME` from that person's home directory, `TMPDIR`
and the `XDG_*` paths from that person's task temporary directory. The spawn then
sets `command.Env` to exactly that map, and a non-nil `Env` is what stops Go from
handing the child the parent's environment.

A credential the daemon holds therefore has no path into a task's shell, and
there is no list of sensitive variable names to keep current: a variable nothing
derived does not exist in the child. The environment boundary and the file
permission boundary are computed from the same resolved identity, so a
deployment where one holds and the other leaks is not expressible.

An external agent CLI runs under the same rule. A turn that drives Claude Code or
Codex goes through `runAsRequester` (`internal/cliharness/harness.go`), which
builds its process through that same `security.CommandRequest` path, and
`commandHarnessFactory` (`internal/harnessselection/selection.go`) refuses to
construct a CLI harness at all without a requester process boundary. Inheriting
this machine's environment is possible only by naming it at the call site
through `AgentCommand.Environment`, which is how the real-agent tests run a
developer's own CLI against their own credentials.

### What the guardrail actually enforces

`TerminalConfiguration` (`internal/config/runtime_configuration.go`) has 9
fields: mode, sandbox provider, workspace root, POSIX helper path, timeout,
output cap, session cap, and the network and interactive-shell switches. None of
them is a list of commands or paths. What the guardrail does is structural:

| Check | Where |
|---|---|
| refuses to execute at all when the daemon is effectively root | `BuildCommandPlan` |
| resolves the working directory against the workspace root | `resolveWorkingDirectoryPath` |
| constructs the environment from the resolved identity and forces a canonical `PATH` | `sanitizeEnvironmentVariables`, `applyPOSIXEnvironment` |
| caps the timeout | `timeoutSecond` |
| requires bubblewrap when `terminal.mode` is `sandbox` | `BuildCommandPlan` |

Executable resolution is a `PATH` lookup, not a permission decision. An absolute
path is resolved verbatim through `EvalSymlinks`; a bare name is searched in the
canonical runtime `PATH` (`resolveExecutablePath`).

Two things in this path *look* like string filters and are not access decisions.
`requesterShellOutcome.failureCode` (`internal/agentruntime/requester_shell.go`)
matches stderr text to classify an already-failed command into a diagnostic
code, and shell arguments are quoted (`shellPathArgument`) as serialization.
Neither runs before the kernel decides.

### Known gaps in the boundary

- The projection is applied only when `terminal.posixHelperPath` is configured.
  With it empty, `applyPOSIXRunner` returns the plan unchanged and everything
  runs as the daemon user. The shipped `config/runtime.standalone.example.json`
  does not set it, so a default standalone deployment runs everything as the
  daemon user on either platform.
- `cmd/bluecollar` deliberately uses `DirectWorkspaceActorFactory`
  (`internal/security/direct_workspace_actor.go`), which has no projection at
  all. It is a single-directory batch runner, not a multi-person daemon.
- `internal/access/access.go` exposes `CanAccess`, consulted before exposing
  capability and MCP tools and before memory reads. This README used to call it
  a migration leftover awaiting deletion. That was wrong: POSIX confines what a
  process may touch on this machine, and says nothing about whether a person may
  send a message as the company or change a shared calendar. Those effects
  happen in `capabilityd`, which takes the requester's identifier for
  attribution and never for authorization, so `CanAccess` is the only thing
  deciding which capability operations a requester may invoke. It sits in front of
  the socket and belongs behind it, which is work to move.
- The POSIX separation tests need root and a setuid helper, so an ordinary
  `go test ./...` skips all three and says nothing about the isolation
  boundary. They run on Linux and macOS
  (`tests/integration/posix_separation_test.go`).

## Configuration

Two files, both passed as flags. `--runtime` points at a runtime configuration
(start from `config/runtime.standalone.example.json`); `--policy` points at a
policy document (`config/policy.example.json`). The policy document is the
source of the people and circles the POSIX projection compiles.

The terminal section is the one that decides whether isolation is on:

```json
{
  "terminal": {
    "mode": "native",
    "workspaceRootPath": "/workspace",
    "posixHelperPath": "/usr/local/bin/blueclaw-posix-helper",
    "timeoutSecond": 600,
    "allowNetwork": true,
    "allowInteractiveShell": false
  }
}
```

The agent section decides which loop runs and what it is allowed to assume:

```json
{
  "agent": {
    "defaultTaskLevel": "low",
    "harness": {
      "name": "claude-code",
      "agentCommandPath": "/usr/local/bin/claude"
    },
    "optionalFileReadPathSuffixes": [".myapp/site.json"]
  }
}
```

`harness.name` is the one in [Harnesses](#harnesses); omit the block and the
bundled loop runs. `defaultTaskLevel` is the effort a task starts at
(`xlow` through `max`) before the tier ladder moves it.
`optionalFileReadPathSuffixes` names files whose absence is a state — a
deployment's own control files, which the daemon knows about and
the harness does not; the default is empty.

External [MCP](https://modelcontextprotocol.io) servers mount into the tool
catalog through `mcpServers` in the same runtime configuration
(`MCPServerConfiguration`, `internal/config/runtime_configuration.go`). Each
server's tools can carry a result contract and policy metadata, so an external
tool participates in the same approval and evidence rules as a built-in one.

## Repository layout

| Path | What lives there |
|---|---|
| `.dependency/bluecollar/agentcontract/` | the harness port and the turn, context, and instruction types both sides compile against |
| `.dependency/bluecollar/toolcontract/` | tool descriptors, registry, validation, kernel tool names |
| `.dependency/bluecollar/taskstate/` | task run, step, event, and artifact stores |
| `.dependency/bluecollar/model/` | language model, chat completion, structured output, and embedding interfaces |
| `agenttest/` | scripted language model for deterministic tests |
| `cmd/` | 10 binaries; see the table below |
| `internal/` | daemon implementation: connectors, agent runtime, security, policy, identity, memory, HTTP, storage |
| `.dependency/bluecollar/` | the agent loop, as its own repository pinned here |
| `internal/acpharness/` | blueclaw as an ACP client, plugging an external agent into the daemon |
| `protocol/` | Zod contracts shared across processes; generates the JSON Schema artifacts |
| `llmd/` | AI SDK sidecar published on its own: structured output and chat generation over a Unix socket |
| `chatd/` | chat bridge and platform adapters (Mattermost, Buzz) |
| `admin/` | Svelte admin and task console sources |
| `web/` | build output of `admin/`, untracked; run `cd admin && bun run build` before serving the console |
| `migrations/` | 29 Postgres migrations, applied in order at boot |
| `tests/` | integration suite and fixtures |
| `lab/` | provisioning and scenario scripts for the development VM |
| `config/` | example policy, runtime, and lab configuration |
| `tools/` | Python sidecars, currently the Graphiti memory daemon |
| `docs/` | [architecture.md](docs/architecture.md) |

| Binary | Purpose |
|---|---|
| `cmd/blueclaw` | the daemon |
| `cmd/blueclaw-posix-helper` | setuid identity switch, POSIX state sync, filesystem operations |
| `cmd/blueclaw-lab` | development VM lifecycle and scenario runner |
| `cmd/blueclaw-supervisor` | boots and watches the virtual-machine guest, proxies host and guest HTTP, handles workspace image sync and restore |
| `cmd/blueclaw-backup`, `cmd/blueclaw-restore` | workspace and database snapshot bundles |
| `cmd/blueclaw-guest-healthd`, `cmd/blueclaw-vsock-http-proxy` | guest health and host-to-guest transport |
| `cmd/bluecollar` | runs the agent loop alone against one directory, for benchmarking; no database, connectors, policy, or POSIX projection |

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

```bash
bun install
bun run test
```

The four TypeScript packages are one Bun workspace, so a single `bun install` at
the root covers them. `bun run test` typechecks, then runs `protocol`, `llmd`,
`chatd`, and `admin` in turn. CI runs exactly these
commands (`.github/workflows/ci.yml`), with Postgres 16 as a service. It checks
out the bluecollar submodule with `SUBMODULE_READ_TOKEN` when that secret
exists, falling back to the repository token — which is enough as soon as
bluecollar is readable without one.

| Test tier | How it runs | Gate |
|---|---|---|
| Unit | `go test ./...`, `bun run test` | none; no external service needed |
| Integration | `go test ./tests/integration/...` | Postgres-backed cases skip unless `BLUECLAW_TEST_POSTGRES_URL` is set |
| Live model | same `go test` invocation | skipped unless `BLUECLAW_LIVE_LLM_TEST=1` is set — these call a real model and cost money |
| External agent CLI | same `go test` invocation | each skips unless `BLUECLAW_TEST_CLAUDE_CODE_PATH`, `BLUECLAW_TEST_CODEX_PATH` or `BLUECLAW_TEST_ANTIGRAVITY_PATH` names the executable — the turn runs on that agent's own credentials |
| Virtual session | `go run ./cmd/blueclaw-lab virtual-session` | requires `--live-llm` or `BLUECLAW_E2E_LIVE=1` |
| Fleet and VM | `go run ./cmd/blueclaw-lab vm-up` | needs the development VM from `config/lab.example.json`; the guest itself boots under the host's fleet lane |

Regenerating the cross-process contracts:

```bash
cd protocol && bun install && bun run generate && bun run build && bun test
```

`AGENTS.md` documents the conventions this codebase holds itself to:
descriptive names over abbreviations, no explanatory comments, one source of
truth per shared contract.

## Project status

Shipped and working: the daemon, the five connectors, the task store and event
ledger, the POSIX projection and setuid helper, the approval gate, the MCP
client and server, mid-run model swapping, the terminal user interface, and five
selectable harnesses.

| Item | State |
|---|---|
| A harness port narrow enough for an external harness | done. Down from nine methods to one, `RunTurn`. Turn routing (deciding whether an inbound message becomes a task at all) and launch-failure completion are daemon policy now and live in `internal/agentruntime`. |
| External harnesses plugging in | done. `agent.harness.name` selects `bluecollar` (in-process), `acp` (any Agent Client Protocol agent), `claude-code`, `codex`, or `antigravity`; see [Harnesses](#harnesses). opencode over ACP and Claude Code over its CLI both run a full turn against this daemon's tool catalog with no harness-specific code beyond a command descriptor. |
| MCP server exposing blueclaw's tool catalog | done. `internal/mcpserver` publishes a per-requester catalog at `/harness/tool-catalog`, authenticated by a session token that is revoked when the turn ends. It is how an external harness reaches tools it may not execute itself. |
| CLI and terminal user interface | done. `cmd/blueclaw-cli` on `charm.land/bubbletea/v2`: task timeline, approval queue, live tool calls, and the enrollment flow. |
| bluecollar moving to its own repository | done. The loop, its routing, the turn stream, and the contract packages live in the bluecollar repository and are pinned here as a submodule. |
| Removal of the `internal/access` Go-side ACL pre-check | withdrawn. It is not a POSIX duplicate; it is the only per-person authorization for capability operations, and nothing downstream repeats it. See Known gaps in the boundary. |

Publishing blockers, in order:

1. **Publish bluecollar in the same release.** It is a private repository, and
   this one requires it, so `go build ./...` fails on a fresh clone by anyone
   outside the organization. Publishing both together is what makes the
   submodule checkout — here and in CI — need no token at all. Everything else
   is secondary to this.
2. Complete a secrets and history audit.
3. Move authorization for capability operations to the service that performs
   them. Not a publishing blocker: the check exists and works.

## FAQ

### Can I run Claude Code or Codex inside blueclaw today?

Yes. Set `agent.harness.name` to `claude-code`, `codex`, `antigravity`, or `acp`
(for any Agent Client Protocol agent) and point `agentCommandPath` at the
executable. The agent starts inside the requester's POSIX identity and reaches
this daemon's tools through an MCP catalog published for that turn only.

Two things to know. The external agent brings tools of its own, so blueclaw
refuses to start it at all unless the POSIX boundary is configured — the kernel,
not a deny list, is what confines it. And the coverage of these adapters is
thinner than the bundled loop's: the live tests are skipped unless the CLI is on
your PATH.

### How is this different from running an agent in a container?

A container isolates the agent from the machine. blueclaw isolates requesters from
each other *inside* the workspace. Two people talking to the same daemon get
different Linux users, different `0700` home directories, and different group
memberships, so one person's agent run cannot read the other's files even though
both run in the same container. blueclaw also
supports bubblewrap when `terminal.mode` is `sandbox`.

### What stops the agent from running a dangerous command?

Nothing in blueclaw, by design. There is no executable allowlist and no denied
command list. The command runs as an unprivileged user with that user's
permissions, and the kernel refuses what that user may not do. Wide or
irreversible tool calls are handled separately, by the approval gate, which
requires a human before the call executes.

### Does it need Linux?

No. Linux is where it is deployed and where the fleet tests run, and
macOS works too, and the same three tests prove it there
([#18](https://github.com/yeomyeonggeori/blueclaw/issues/18)). The helper creates
identities through `dscl` and `dseditgroup` on macOS and through `useradd`,
`groupadd` and `usermod` on Linux, and it reads the account database from
Directory Service, which on macOS is the only place every account appears;
`/etc/passwd` there lists system accounts alone.

Two differences are forced by the platform. macOS ships no `/usr/sbin/nologin`,
so projected people get `/usr/bin/false`, which is what its own service accounts
use. And macOS hides accounts below uid 500 from the login window while the
projection allocates from 100000, so each record carries `IsHidden`.

What macOS does not have is bubblewrap, so `terminal.mode` stays `native` there.
That is optional narrowing, and the boundary is the POSIX identity either way.

### Do I need Postgres and a chat platform?

Postgres, yes; the task store and event ledger live there. A chat platform, no.
The `api` connector accepts a JSON POST and returns replies over HTTP, which is
enough to drive a task run from `curl`.

### Is there a binary release or a Docker image?

No. There is no Makefile, Dockerfile, service unit, or release binary in this
repository. Build from source with `go build ./...`.

## Contributing

Pull requests open at alpha. Until then the design is moving too fast for
outside patches to be a kindness to whoever sends them. Issues are welcome now
— tell us what broke. For a security problem, follow [SECURITY.md](SECURITY.md)
instead of opening an issue.

## License

Apache License 2.0. See [LICENSE](LICENSE).

The bundled agent loop is a separate project under the same licence:
[bluecollar](https://github.com/yeomyeonggeori/bluecollar) is Apache 2.0 as
well, pinned here as a submodule at `.dependency/bluecollar`. A build that ships
the bundled harness ships both.

The Mattermost adapter under `chatd/src/adapters/mattermost/` vendors
MIT-licensed third-party code; its license is kept alongside it.
