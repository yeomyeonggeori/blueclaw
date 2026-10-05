<img src="assets/blueclaw.logo.svg" alt="blueclaw" width="112">

# blueclaw

*A self-hosted meta-harness: it runs agent harnesses such as bluecollar, Claude Code and Codex for everyone in a company, and supplies what a harness cannot have on its own.*

[![CI](https://github.com/yeomyeonggeori/blueclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/yeomyeonggeori/blueclaw/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

> **Status: pre-alpha.** The interfaces, the wire grammar, the configuration keys and the database schema change without notice. Pin a commit and expect to read diffs.

<img src="assets/screenshots/tui-tasks.png" alt="blueclaw-cli showing five task runs, one waiting for approval" width="100%">

```bash
git clone --recursive https://github.com/yeomyeonggeori/blueclaw.git
cd blueclaw
go build ./...
```

## What a meta-harness is

A harness runs an agent loop for the person at its terminal. It reads the prompt, plans, calls tools, asks before a risky call and answers. bluecollar, Claude Code, Codex and pi are harnesses.

A meta-harness is a harness that runs harnesses. The harness it runs does the agent's work: planning, choosing tools, deciding what a reply means for the task. blueclaw supplies what that harness lacks once a whole company shares one agent.

| blueclaw supplies | so that |
|---|---|
| conversations from chat platforms, HTTP and ACP clients, each bound to a harness session | people reach the agent where they already talk, and a thread keeps its context |
| the person behind every message, resolved against a policy | each tool call runs as that person's own unprivileged POSIX user |
| the company's tools and skills, published over MCP | every harness works from the same catalog |
| a question for a person, carried to their thread, with their reply read back as one of the offered answers | a harness can ask someone who is not at its terminal, and an irreversible call runs only on that person's recorded approval |
| a durable ledger of every task, and sessions that outlive a restart | work and its evidence survive the process |
| schedules | work runs when nobody is talking |

What belongs in blueclaw follows from that definition. When a good harness already does something, blueclaw runs that harness and leaves the job to it. A feature that only works by reaching inside one harness belongs in that harness. bluecollar is the default harness, and any ACP or CLI agent can take its place.

The [quickstart](https://blueclaw.intern.kim/docs/quickstart) configures the standalone runtime and policy before starting the daemon. The full reference is [DOCS.md](DOCS.md), published at [blueclaw.intern.kim](https://blueclaw.intern.kim).

| path | holds |
|---|---|
| `cmd/` | the daemon, the setuid POSIX helper, the terminal client, backup and restore, the scenario runner |
| `internal/` | connectors, intake, agent runtime, approvals, security, policy, identity, memory, scheduler, HTTP |
| `.dependency/bluecollar` | [bluecollar](https://github.com/yeomyeonggeori/bluecollar), the default harness and the shared `agentcontract` |
| `.dependency/bluememo` | [bluememo](https://github.com/yeomyeonggeori/bluememo), the memory store |
| `migrations/` | Postgres migrations, embedded and applied in order at boot |
| `protocol/` | Zod contracts shared across processes and the JSON Schemas generated from them |
| `chatd/` | the chat bridge and its Buzz and Mattermost adapters |
| `admin/` | the Svelte admin and task console |
| `config/` | example runtime and policy configuration |
| `lab/` | a live task-classification scenario for `blueclaw-lab virtual-session --scenario-file` |
| `tests/` | integration suite and fixtures |
| `docs/` | the documentation site, generated from `DOCS.md`, and the generated tool catalog |

## Contributing

Pull requests open at alpha. Issues are welcome now. For a security problem, follow [SECURITY.md](SECURITY.md) instead of opening an issue. `AGENTS.md` holds the conventions the code follows.

## License

Apache License 2.0. See [LICENSE](LICENSE).

The default harness, [bluecollar](https://github.com/yeomyeonggeori/bluecollar), is a separate project under Apache 2.0 as well, pinned here as a submodule. A build that ships the bundled harness ships both.

The Mattermost adapter under `chatd/src/adapters/mattermost/` vendors MIT-licensed third-party code; its license is kept alongside it.
