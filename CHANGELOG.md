# Changelog

## v0.0.6 - 2026-10-25

- Update the submission version and release metadata to `v0.0.6`.
- Include the complete project submission metadata: author, source scope, run commands, architecture/module specifications, and single-person responsibility for all work by 王鹏斐.
- Add the submission metadata document to the documented release package contents.

### Validation

- Existing v0.0.5 Claude Code, sandbox, proxy, and authorization behavior is retained.
- Documentation-only release follow-up; the repository's macOS CI, race tests, vet, and coverage gate remain required.

## v0.0.5 - 2026-10-25

- Complete the project submission metadata: author, owner, source scope, run instructions, architecture/module specifications, and single-person responsibility are documented in `docs/project-submission.md`. All work is credited to 王鹏斐.
- Add a non-interactive Claude Code CLI path with a host-only Anthropic Messages proxy. The sandbox receives a per-run local capability, while the upstream API Key stays in the host Keychain. Anthropic authorization is separate for the `claude` agent.
- Scope Claude's extra Seatbelt read grant to its private per-user runtime directory. Keep the startup denial probe mandatory; configure the KeyMoat MCP explicitly and clean up temporary sessions on exit or cancellation.
- Stream OpenAI and Anthropic SSE responses through a bounded cross-chunk credential guard instead of buffering the full stream. JSON responses remain buffered and checked before forwarding.
- Reorganize the README around setup and use, and document the implementation, test scope, and security limits separately.

### Validation

- macOS race tests, `go vet`, and the credential-free demo pass. A locally installed Claude Code CLI consumes a simulated Messages completion through the sandbox and host proxy; no real API credential is read.
- Combined process statement coverage: 90.5% locally; the no-Claude-CLI check also meets the 90% CI threshold.
- Release binaries are experimental and not code-signed or notarized. Real upstream requests and cross-version Claude Code compatibility are not verified by automation.

## v0.0.4 - 2026-10-25

- Deliver the Codex single-path end-to-end workflow: real Codex CLI starts inside KeyMoat, uses an ephemeral local configuration, reaches only the host model proxy, and consumes a simulated Responses completion in CI.
- Deliver the loopback authorization console: native folder selection, OpenAI/GitHub switches, repository grants, Codex task launch/cancel, bounded output, CSRF and same-origin checks, and shutdown cleanup.
- Deliver the reproducible adversarial demo: writable project access succeeds, read-only and outside access fail, scoped GitHub access is revoked live, and the model proxy never exposes the upstream credential.
- Keep the implementation experimental: real OpenAI/GitHub credentials are never used by automated tests; online Codex execution requires the host Keychain setup documented in `README.md`.

### Validation

- macOS race tests and `go vet` pass.
- Combined process statement coverage: 91.3% locally; CI enforces at least 90%.
- Release binaries are experimental and not code-signed or notarized.

## v0.0.3 - 2026-10-25

- Normalize the Go runtime layout: CLI dispatch, sandbox runtime, authorization, credential storage, Codex, GitHub, broker, and MCP responsibilities now have explicit file boundaries.
- Document the code layout and dependency direction in `docs/architecture.md`.
- Preserve the Codex MVP, loopback authorization console, scoped GitHub MCP, and no-secret adversarial demo behavior from v0.0.2.

### Validation

- macOS race tests and `go vet` pass.
- Combined process statement coverage: 91.3% locally; CI enforces at least 90%.
- Release binaries are experimental and not code-signed or notarized.

## v0.0.2 - 2026-10-25

- Adopt **KeyMoat** as the project display name and replace the numeric logo with a key-and-boundary mark. The `1024` CLI, module path, repository URL, and v0.0.1 artifacts remain unchanged for compatibility.
- Add a single-path Codex runner with host-only OpenAI Responses proxy and an ephemeral local session capability. GitHub MCP can read scoped UTF-8 files from explicitly authorized repositories.
- Add a credential-free demo and simulated model integration tests. Real upstream access still requires user-provided credentials and is not covered by CI.
- Add a host-only KeyMoat control console for folder selection, connection/repository authorization, and Codex task launch/cancellation.

### Validation

- macOS race tests and `go vet` pass.
- Combined process statement coverage: 90.6% locally; CI enforces at least 90%.
- Release binaries are experimental and not code-signed or notarized.

## v0.0.1 - 2026-10-24

First experimental macOS release of 1024.

### Included

- Per-run Seatbelt isolation with default-denied file and network access, writable and read-only folder grants, and launch-time denial probes.
- A per-run Unix socket broker and keyless MCP stdio tools for sandbox status and a narrowly scoped GitHub identity lookup.
- Host-side GitHub credential storage in macOS Keychain with independent Claude Code, Codex, and dsh authorization switches.
- macOS CI with race detection, `go vet`, and a **90% minimum combined statement coverage** gate. The local release baseline is **90.7%**.
- Project mark, architecture, security-boundary, and testing documentation.

### Limitations

This is a prototype, not a production security boundary. Agent names are labels; online Claude Code, Codex, and dsh sessions, a desktop authorization UI, general MCP credential proxying, and GitHub repository operations are not implemented. Tests use a simulated GitHub API and do not store or read a real token. See [security boundaries](docs/security.md) and [testing](docs/testing.md).
