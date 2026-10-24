# Changelog

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
