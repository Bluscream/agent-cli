# Incremental audit and repair

Baseline: `ea872730f921a870fa585f2ec63f4e13f501ac55`.

This is a working audit log, not a declaration that every issue is fixed. The scope includes correctness, CLI behavior, data compatibility, safety, structure, duplication, performance, documentation, and tests. Live commands are read-only; mutation reproductions use temporary fixtures. The installed `ai` executable has not been replaced.

## First repair batch

- MCP: reject malformed/non-object config before writing any target; preserve unknown server fields and large JSON integers; atomic per-file replacement; preserve existing file permissions and use 0600 for new configs; report write failures; fix enable/remove of legacy disabled servers; avoid reactivating destination-disabled servers during sync.
- Structure: split MCP serialization from management and consolidate repeated mutation loops. Split each provider by responsibility rather than retaining 900–1200-line files. Replace three directory-copy implementations with `fsutil`.
- Files: reject plugin-removal traversal names in every provider; use confined directory operations; reject directory-copy escapes, recursive destinations, symlinks in input, and special files; preserve executable permissions.
- Accounts: reject mutations addressed to providers other than Antigravity; validate profile names; use private profile file permissions. The full switch/save transaction remains a follow-up.
- Recovery: reject undecodable indices and use a SQLite snapshot backup rather than copying the main database file without its WAL. Guard protobuf length conversion against integer overflow.
- History: read Codex conversation and memory rows as JSON instead of splitting multiline data on newlines; filter workspace before applying limits; avoid short-ID slice panics.
- Transcripts: remove fixed 400/600-byte content truncation in all providers. Tool fidelity, scanner errors, pagination, and structured handoff completeness still need work.
- Selection: respect `log --last --provider`; resolve explicit handoff IDs across providers; validate provider aliases in models/limits; return JSON for empty `lasts`; avoid inventing an active Codex account when its auth file is absent.
- Other correctness: preserve case when parsing RFC3339 dates; fix minute/minutes normalization; avoid inspecting the current repository for an empty conversation workspace.
- Staticcheck: remove unused detail-width helper, redundant bitwise operation, and deprecated `strings.Title`; remove an unused account bookkeeping map.

## Verification

The original tests passed despite the reproduced defects. New regression checks cover MCP malformed input, unknown fields, integer preservation, legacy disabled-server transitions, error reporting, and validation before writes; filesystem traversal/symlink containment and executable modes; protobuf overflow; timestamp/minute parsing; invalid provider selection; empty JSON; Codex multiline rows and full message content; Claude short filenames and full message content.

Commands used: `go test -race ./...`, `go vet ./...`, release/debug builds, and Staticcheck v0.8.1. Read-only CLI smoke results are recorded in `smoke-results.json`. All 30 recorded smoke invocations exited successfully; data commands emitted valid compact JSON. Root help/version and shell completion intentionally emit text. Live Codex history now matches all six database rows, and provider-specific latest-log selection was checked.

The local `go` launcher runs inside Distrobox, which lacks `sqlite3`, so that environment skips the SQLite fixture test. The compiled Codex test binary was also run directly on the host, where both the multiline database and full-content tests passed. `make check`, race tests, debug tests, Staticcheck, and `git diff --check` passed.

A zero exit status alone is not proof of semantic correctness, especially for remaining recovery stubs.

Baseline vulnerability scan: no reachable dependency vulnerabilities. It found GO-2026-5970 in the required `golang.org/x/text` module, but not in imported vulnerable packages. The scanner selected Go 1.26.8; that result does not establish the security of the originally installed Go 1.26.0 binary.

Baseline coverage collection encountered a local missing `covdata` tool. Ordinary and race tests ran successfully. Partial baseline coverage showed CLI 3.3%, Antigravity 12.6%, MCP 61.2%, Git 79.3%, ID utilities 87.5%; Codex and Claude had no tests. These are baseline figures, not post-repair coverage.

## Remaining work, in order

### Data mutations and recovery

- Make account save/switch/fresh transactional, validate allowed DB keys, propagate every DB failure, avoid stale token merges, quote desktop entries, and replace broad `pkill -f` with ownership-aware process control.
- Reject all malformed protobuf encodings, including partial parses/varint overflow; preserve unknown outer fields. Recovery must compare the index before committing and avoid classifying unreadable data as a ghost. Remove fabricated workspace/timestamp defaults. Handle a missing index row explicitly.
- Replace Codex/Claude recovery stubs with honest unsupported/diagnostic results. Never report a healthy database without checking it.
- MCP still needs concurrency conflict detection, provider-specific schemas and scope, conflict reporting, explicit target configuration, secret redaction, and coherent partial-success output. Atomic replacement of one file is not a transaction across all targets. JSONC input is safely rejected, not supported.
- Complete single-file plugin-copy safety, transactional installation, manifest/registration handling, and accurate installed/enabled state. Codex currently installs into a location its own listing ignores.
- Redesign memory sync around provenance, full IDs, deduplication, conflict policies, and provider-compatible storage. Repeated sync must be idempotent. Antigravity backup currently captures summaries rather than all knowledge artifacts. Claude purge includes global instructions. Add restore and scoped preview operations.
- Skill sync must distinguish builtin/custom skills, prevent name collisions, validate compatibility, and surface failed imports. `skill <name>` currently ignores the name. Discovery misses plugin-provided and symlinked skills.

### CLI and data model

- Centralize provider selection and argument validation; remove shadowed global/local flags. Resolve ambiguous prefixes instead of selecting the first match. Define `--last`, zero/negative limits, and mutually exclusive mutations consistently.
- Make JSON/compact output consistent for mutations, errors, empty arrays, and debug data. CSV currently mixes data with footnotes/headings in several commands. Apply color options before constructing styled values; keep ANSI sequences out of machine output.
- Return partial errors explicitly instead of silently skipping provider failures. Stop representing unavailable counts/timestamps/quotas as real zeros/current times.
- Separate full transcripts from previews and summaries. Preserve tool calls/results, roles, channels, call IDs, and timestamps. Add scanner-error handling and bounded/streaming reads without silently losing long records.
- Handoffs need the actual initial goal, latest user instruction, complete final response, source provenance, open tasks, and accurate touched artifacts. Current heuristics do not establish task completion.
- Remove guessed model context windows/tiers and guessed quota resets; expose source, age, and uncertainty. Properly parse TOML rather than scanning `model =` lines.
- Correct process CPU units and aggregation, `/proc/stat` parsing with spaces in process names, substring false positives, busy heuristics, and installation discovery.
- Git: include staged diff, handle URI escaping and root paths, report command failures, add timeout/cancellation, and distinguish conflicts and unknown state.

### Architecture, performance, and maintenance

- Split the oversized provider interface into optional capabilities. Inject paths, clocks, process runners, and provider collections rather than depending on HOME/global registration throughout.
- Avoid rescanning all histories for every detail lookup; cache within one command, push filters into queries, and avoid repeated subprocess/database reads. `plugins list/status` spends roughly 2.3 seconds scanning an AppImage with `strings`.
- Replace hardcoded personal paths and external script assumptions with discovery/configuration and actionable diagnostics. Respect applicable environment/XDG locations.
- Expand contract tests across commands/formats and providers; include malformed/oversized records, missing dependencies, permissions, cancellation, absent providers, ambiguous IDs, and failed writes. Add actual sync tests: the original test named AddRemoveSync did not exercise sync.
- Add non-mutating format validation, optional static analysis and vulnerability targets, CI, toolchain/version provenance, and release documentation. Refresh README command coverage, unsupported features, prerequisites, schema assumptions, and recovery limits.

## Read inventory

The baseline Go files, tests, Makefile, module metadata, README, gitignore, and `.references/prompt.md` were inspected. Generated binaries were exercised and their build metadata inspected rather than treated as source. External scripts referenced by the project have not received a full audit; their behavior remains part of the unfinished integration review.
