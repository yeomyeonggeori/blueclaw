# Blueclaw Lab

This directory holds the local test rig assets for `blueclaw-lab`.

This is the self-contained lane: it needs only this repository, so it is the
way to bring up a full guest without the appliance tooling, which is not
published. The appliance repository runs its own fleet lane on Apple
`container`; that lane reuses `lab/scripts/` but none of the Tart setup below.
Neither lane runs in CI — Tart and a hardware-virtualised guest cannot start on a hosted runner —
so treat these commands as unverified by the build and report breakage.

The default topology is:

- Apple Silicon macOS host
- Tart ARM Linux virtual machine
- Blueclaw inside a guest the Linux virtual machine boots under Cloud Hypervisor

The host is the companion/browser side.
The Linux virtual machine is the simulated `InternKim`.
The guest is the simulated production `Blueclaw` runtime boundary.

Useful commands:

```bash
go run ./cmd/blueclaw-lab --configuration config/lab.example.json image-build
go run ./cmd/blueclaw-lab --configuration config/lab.example.json vm-up
go run ./cmd/blueclaw-lab --configuration config/lab.example.json scenario-mattermost
go run ./cmd/blueclaw-lab --configuration config/lab.example.json scenario-slack
go run ./cmd/blueclaw-lab --configuration config/lab.example.json scenario-browser-handoff
go run ./cmd/blueclaw-lab --configuration config/lab.example.json vm-down
```

Connector scenarios should exercise the unified connector runtime rather than platform-specific handler shortcuts.

- `scenario-mattermost` covers Mattermost-style receive and reply paths
- `scenario-slack` covers Slack Events API-style receive and Slack Web API-style reply paths
- further receivers, such as Socket Mode, should be added as `ConnectorTransport` implementations without changing the connector core

`go run ./cmd/blueclaw-lab virtual-session` drives the agent loop without any
virtual machine; see the repository README.
