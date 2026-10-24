# Changelog

## Unreleased

- Adopt **KeyMoat** as the project display name and replace the numeric logo with a key-and-boundary mark. The `1024` CLI, module path, repository URL, and v0.0.1 artifacts remain unchanged for compatibility.

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
