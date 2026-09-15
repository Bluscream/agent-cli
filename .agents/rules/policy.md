# Agent Guidelines & Code Policy

## Code Size Limits
- **Function Limit**: Functions and method implementations must be kept concise (target <= 100 physical lines). Long functions must be split into logical helper functions.
- **File Limit**: Code files must not exceed 1,000 physical lines. Modules exceeding 1,000 lines must be split into focused domain files (e.g. `limits.go`, `models.go`, `conversations.go`).
- **Enforcement**: Run `go run ./internal/codecheck` or `make codecheck` to verify code size policies.

## Credential & Secret Protection
- **No Credentials**: Never commit or push secrets, tokens, API keys, private passwords, or private key material to Git.
- **Local State Safe Handling**: Provider configs and SQLite databases in user homes (`~/.config`, `~/.claude.json`, `~/.codex`) must be accessed in read-only mode during inspection.
- **Enforcement**: Run `make secrets-check` (or `gitleaks detect`) prior to pushing.

## Verification Gate & Deployment
- Always run `scripts/build.sh` before committing changes.
- To deploy to `~/.local/bin/ai`, run `scripts/build.sh --deploy`. Never bypass failed gates.
