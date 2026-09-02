# Blueclaw Protocol

This package defines Blueclaw cross-process contracts with Zod and produces deterministic JSON Schema artifacts.

```bash
bun install
bun run generate
bun run generate:check
bun run build
bun test
```

`src/` is the contract source and `bun.lock` pins generation. Manifests, schemas, and hashes are computed from Zod through the `@blueclaw/protocol/artifacts` export. `bun run generate` writes tracked release artifacts to `generated/`; `bun run generate:check` rejects stale or extra files.

This package defines the shape of a capability descriptor and defines no tool. A product authors its own tool catalog, hashes it into a manifest through `buildProtocolManifest`, and offers it to this runtime's scenarios through `BLUECLAW_SCENARIO_CAPABILITY_CATALOG`.

Breaking changes require a protocol version bump. Each fixture bundle maps a schema case name to one or more documents so TypeScript and Go tests share compatibility evidence without a directory of one-case files.

Zod is the canonical cross-process contract. Go DTOs are runtime implementation types and must validate generated schemas at process boundaries as each contract migrates. Do not weaken Zod schemas to match permissive decoding.
