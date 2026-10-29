# Security Policy

## Reporting a vulnerability

Email **lee@dawn.kim**. Please do not open a public issue for a security
problem.

Include what you need to make the problem reproducible: the version or commit,
the configuration involved, and the steps that trigger it. A proof of concept
helps but is not required.

You will get an acknowledgement, and we will tell you what we found and when a
fix ships. Please give us a chance to release a fix before disclosing publicly.

## What we consider a vulnerability

Blueclaw's security claim is that a person's own POSIX identity, not a string
filter, decides what the agent can reach on their behalf. Anything that breaks
that is in scope:

- Acting on the workspace as a UID, GID, or supplementary group the requester does not hold.
- Reading or writing `/workspace/.blueclaw`, another person's private directory, or a protected directory the requester's groups do not reach.
- Escaping the requester identity through `blueclaw-posix-helper`, the terminal, or any tool that routes through it.
- Performing an approval-gated action without the approval, or replaying an approval outside its task.
- Leaking platform credentials, provider keys, or memory contents across people, conversations, or tenants.
- Prompt injection that reaches any of the above rather than only affecting the model's own wording.

Out of scope: reports that a command failed at the kernel rather than being
blocked earlier — that is the intended design — and findings that require
already having root on the appliance.
