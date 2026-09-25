<img src="assets/blueclaw.logo.svg" alt="blueclaw" width="112">

# blueclaw

*A self-hosted agent host: each requester's tool calls run as their own unprivileged POSIX user, side effects wait at an approval gate, and every step lands in a durable ledger.*

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

The [quickstart](https://blueclaw.intern.kim/docs/quickstart) configures the standalone runtime and policy before starting the daemon. The full reference is [DOCS.md](DOCS.md), published at [blueclaw.intern.kim](https://blueclaw.intern.kim).

| path | holds |
|---|---|
| `cmd/` | the daemon, the setuid POSIX helper, the terminal client, the guest supervisor, backup and restore, the lab runner |
| `internal/` | connectors, intake, agent runtime, approvals, security, policy, identity, memory, scheduler, HTTP |
| `.dependency/bluecollar` | [bluecollar](https://github.com/yeomyeonggeori/bluecollar), the bundled agent loop and the shared `agentcontract` |
| `.dependency/bluememo` | [bluememo](https://github.com/yeomyeonggeori/bluememo), the memory store |
| `migrations/` | Postgres migrations, embedded and applied in order at boot |
| `protocol/` | Zod contracts shared across processes and the JSON Schemas generated from them |
| `chatd/` | the chat bridge and its Buzz and Mattermost adapters |
| `admin/` | the Svelte admin and task console |
| `config/` | example runtime, policy and lab configuration |
| `lab/` | provisioning and scenario scripts for the development VM |
| `tests/` | integration suite and fixtures |
| `docs/` | the documentation site, generated from `DOCS.md`, and the generated tool catalog |

## Contributing

Pull requests open at alpha. Issues are welcome now. For a security problem, follow [SECURITY.md](SECURITY.md) instead of opening an issue. `AGENTS.md` holds the conventions the code follows.

## License

Apache License 2.0. See [LICENSE](LICENSE).

The bundled agent loop, [bluecollar](https://github.com/yeomyeonggeori/bluecollar), is a separate project under Apache 2.0 as well, pinned here as a submodule. A build that ships the bundled harness ships both.

The Mattermost adapter under `chatd/src/adapters/mattermost/` vendors MIT-licensed third-party code; its license is kept alongside it.
