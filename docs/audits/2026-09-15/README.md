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

## Antigravity transcript repair (2026-09-16)

- Fixed missing/null message content being rendered as the literal `null`. Literal user/assistant text containing `null` remains intact.
- Preserve every exported tool call and its raw JSON arguments, including multiple calls in one planner step. Separate narrative from calls so filtering tools does not remove assistant text. Arguments are available in text and JSON output without numeric rounding.
- Classify model-sourced execution steps as tool results so `log --tools` shows their output without requiring `--system`. Hide tool calls/results by default, before applying the displayed-turn limit. Calls without arguments remain visible with `--tools`.
- Tool-only planner steps no longer replace the handoff's last assistant response. Missing/invalid timestamps remain unknown instead of being fabricated as the current time.
- Regression fixtures cover the observed Antigravity schema, null versus literal text, multiple calls, structured/long content, exact JSON integers, tool/system visibility, tail limits, JSON output, and handoff response selection. Provider data is read-only; tests use temporary HOME/transcript directories.
- This does not finish transcript fidelity across all providers: scanner-error handling, malformed-record diagnostics, call/result correlation where identifiers exist, and Claude/Codex tool parsing remain follow-ups.

## Claude Desktop profile management (2026-09-27)

- Added profile save, switch, fresh, and listing for Claude Desktop in `internal/provider/claude/account.go`.
- Implemented maximum surgical precision targeting only values bound to authentication, keeping all other state intact:
  - Chromium cookies (`~/.config/Claude/Cookies`): surgical extraction and replacement of auth-only rows (`sessionKey`, `sessionKeyV3`, `sessionKeyLC`, `sessionKeyV3LC`, `__Host-ant_trusted_device`, `lastActiveOrg`, `routingHint`, `activitySessionId`), strictly preserving Cloudflare clearance (`cf_clearance`, `__cf_bm`), cookie consent preferences (`anthropic-consent-preferences`), theme, and sidebar visibility across accounts and fresh logins.
  - Electron app configuration (`~/.config/Claude/config.json`): key-level extraction and restoration of auth tokens (`lastKnownAccountUuid`, `oauth:tokenCache`, `oauth:tokenCacheV2`, `dxt:allowlist*`), preserving window geometry, zoom, locale, and UI preferences.
  - Active OAuth metadata (`~/.claude.json`): key-level merge of `oauthAccount` and `userID`, strictly preserving custom MCP servers (`mcpServers`) and project configs.
  - Device registry (`~/.config/Claude/ant-device-registry.json`): non-destructive JSON map merge so registered device tokens for multiple account UUIDs coexist.
  - Storage exclusions: `IndexedDB` (~8.1 MB offline cache) and `Session Storage` are completely excluded; `Local Storage` is untouched so drafts, prompt inputs, and side-pane UI layouts stay intact across switches.
  - Global MCP configuration (`claude_desktop_config.json`) and plugins are strictly preserved and untouched.
- Replaced broad `pkill -f` with user ownership-aware process control (`-u <uid>`) with graceful SIGINT -> SIGTERM -> SIGKILL transitions and stale lock cleanup (`SingletonLock`, `SingletonCookie`, `SingletonSocket`, `Cookies-wal`, `Cookies-journal`).
- Updated CLI (`internal/cli/account.go`) to support `-p claude`, with automatic provider resolution in `ai account switch <name>` when the profile name is unique across providers, and ambiguity detection when identical profile names exist.
- Updated `ai account list` to show all saved Claude profiles with `ACTIVE` or `AVAILABLE` status.
- Desktop shortcut creation (`~/Desktop/claude-<name>.desktop`) pointing to `ai account switch <name> -p claude`.
- Unit tests in `internal/provider/claude/account_test.go` and `internal/cli/account_test.go` test isolation, private file permissions (0700/0600), non-destructive state restoration, surgical cookie queries, and validation.

## Conversation lookup ergonomics (2026-09-27)

Found while using the CLI to locate a conversation from a half-remembered title
and a quoted phrase. Each item below cost an avoidable extra command or
published a wrong number.

- Message counts no longer report the position at which a partial scan stopped. `parseClaudeTranscript` stops reading once it has the title and workspace, and then published that stopping point (10, or the 40-line cap) as `messages_count`; a 3300-record transcript reported 10. Listing now reports `provider.CountUnknown` (-1) unless it reached the end of the file without a scanner error, `GetConversation` reports the exact record count returned by `readClaudeTurns`, and the Claude session-index path no longer invents a count of 1. Codex's listing uses the same sentinel in place of its previous 0. `ai history` and `ai lasts` render an unknown count as `-` rather than as a number.
- Search results carry the raw conversation ID. `MatchResult` gained `full_id` alongside the 8-character `entity_id`, so a consumer can address a conversation without a second resolution step. Short IDs already resolved in `ai log` and `ai conversation`; only the canonical value was missing from the output.
- `ai log` gained `--grep`, `--grep-pattern`, and `--context`/`-C`. Locating a phrase inside a 664-turn log previously meant dumping the whole transcript and filtering it outside the tool. Matching reuses `search.Matcher`, covers content, thinking, and tool names, and both text and JSON output report how many turns matched on their own.
- Title search matches every term in any order. `ai history --keyword` and the new `ai search --fuzzy` no longer require a remembered title to be a contiguous substring of the real one. When a title search finds nothing, `ai history` names `ai search` instead of printing an empty table.
- Fixed in passing: `QuickBytesMatch` lowercased the haystack but not the needle, so a case-sensitive search for text containing capitals matched nothing and skipped each transcript before its turns were read.

Regression tests cover the partial-scan count against a 137-record fixture, full IDs in search results, grep/context/regex/no-match behavior in `ai log`, out-of-order and absent-term keyword matching, the empty-result suggestion, and case-sensitive quick matching. All use temporary HOME directories and fixtures; no real conversation data is written to.

## Ingestion moved into the CLI (2026-09-27)

- `ai ingest` publishes transcripts from every provider into a Qdrant collection, replacing the separate TypeScript `ai-history-mcp` daemon, which had not ingested anything since 2026-08-30 and whose systemd unit is now renamed out of systemd's view. Rather than porting its three bespoke transcript parsers, ingestion reuses the providers already here, so it inherits their parsing and records each conversation's real workspace instead of the daemon's own working directory.
- Opt-in by environment: without `AI_INGEST_QDRANT_URL` the command explains what to set and exits successfully. Configuration lives in `~/.config/environment.d/30-ai-ingest.conf`.
- Points are payload-only. The daemon wrote a 1536-dimension zero vector on every point and queried with a full-text payload filter, never a vector search, so the embeddings it implied never existed; writing none keeps the same search behaviour without the storage. Qdrant still demands a vector field, so each point carries an empty object, and ingest refuses to write into a collection that declares real vectors.
- Point IDs derive from provider, session, step index and an ordinal within the step, leaving out the timestamp the daemon hashed in, so re-ingesting updates a turn in place instead of duplicating it whenever a timestamp reparses differently. The ordinal was added after the before/after counters showed 426 fewer points stored than sent: Antigravity emits a planner step's narrative and each of its tool calls as separate turns under one `step_index`, so keying on the step alone silently overwrote all but the last — one conversation stored 1712 of its 1918 turns while reporting success. The ordinal is omitted at zero, so the identities that were already correct did not change and a forced re-ingest backfilled exactly the missing 426 points.
- `--watch` keeps the process running via fsnotify, coalescing bursts of appends into one pass. `cmd/ai` now cancels the command context on SIGINT/SIGTERM so long-running commands stop cleanly. `TranscriptRooter` is an optional capability interface rather than another method on `Provider`, per the remaining-work item about that interface being oversized.
- `ai history`, `ai search` and `ai log` accept `--remote` to read the collection instead of local transcript files, including short-ID resolution via the facet endpoint. Fields the collection does not carry render as `-`: `sizeCell` now treats a negative size as unknown. `--remote` rejects `--pattern` rather than ignoring it, because matching goes through Qdrant's full-text index.
- Progress reporting: a bar, running point total, elapsed time and an ETA extrapolated from completed conversations, plus a summary ending in the collection's point count either side of the pass. The bar draws only on a terminal, and widths are measured in runes because each bar glyph is three bytes.

The Qdrant REST calls are hand-written against `net/http` rather than using the official client, which is gRPC-first and pulls grpc, protobuf and genproto into a module that otherwise has nine dependencies — a poor trade for five JSON calls. `fsnotify` is a real dependency.

Regression tests use an HTTP test double that evaluates filters, ordering and the paging cursor, so no test touches a real instance. An earlier version of that double returned every stored point regardless of the filter, which made the query tests pass without exercising anything.

## Size limits are now enforced (2026-09-27)

`internal/codecheck` existed and worked but nothing ran it: it was absent from `scripts/build.sh`, `make check` and `.githooks/`, and the tree had drifted to 34 oversized functions. It is now the `size-limits` step of the gate and a `make size-limits` target.

Enforcing it outright would have failed on code nobody is touching, including work in progress, so `.codecheck-baseline` records the functions that were already oversized along with the size each had. The list can only shrink: an unlisted breach fails, a listed function that grew past its recorded size fails, and a listed function that now fits fails as stale so the entry gets deleted. Files past 600 lines are reported as a notice rather than a failure. The checker is now split into `scan.go`, `baseline.go` and `main.go` with tests covering each failure mode, and the gate was verified to reject a deliberately oversized function.

Splitting the 34 baselined functions — mostly cobra constructors whose `RunE` closure holds the whole command body — remains outstanding.

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
- Return partial errors explicitly instead of silently skipping provider failures. Stop representing unavailable counts/timestamps/quotas as real zeros/current times. Conversation message counts now use the `CountUnknown` sentinel; timestamps and quotas still fabricate values.
- Separate full transcripts from previews and summaries. Preserve tool calls/results, roles, channels, call IDs, and timestamps. Add scanner-error handling and bounded/streaming reads without silently losing long records.
- Handoffs need the actual initial goal, latest user instruction, complete final response, source provenance, open tasks, and accurate touched artifacts. Current heuristics do not establish task completion.
- Remove guessed model context windows/tiers and guessed quota resets; expose source, age, and uncertainty. Properly parse TOML rather than scanning `model =` lines.
- Correct process CPU units and aggregation, `/proc/stat` parsing with spaces in process names, substring false positives, busy heuristics, and installation discovery.
- Git: include staged diff, handle URI escaping and root paths, report command failures, add timeout/cancellation, and distinguish conflicts and unknown state.

### Architecture, performance, and maintenance

- Split the oversized provider interface into optional capabilities. `TranscriptRooter` is the first of these, added for ingestion's watch mode; the rest of `Provider` is unchanged. Inject paths, clocks, process runners, and provider collections rather than depending on HOME/global registration throughout.
- Avoid rescanning all histories for every detail lookup; cache within one command, push filters into queries, and avoid repeated subprocess/database reads. `plugins list/status` spends roughly 2.3 seconds scanning an AppImage with `strings`.
- Replace hardcoded personal paths and external script assumptions with discovery/configuration and actionable diagnostics. Respect applicable environment/XDG locations.
- Expand contract tests across commands/formats and providers; include malformed/oversized records, missing dependencies, permissions, cancellation, absent providers, ambiguous IDs, and failed writes. Add actual sync tests: the original test named AddRemoveSync did not exercise sync.
- Add CI, toolchain/version provenance, and release documentation. Format validation, static analysis, vulnerability and secret scanning, and size limits are all gate steps now; README command coverage is current as of 2026-09-27. Unsupported features, prerequisites, schema assumptions, and recovery limits still need documenting.

## Read inventory

The baseline Go files, tests, Makefile, module metadata, README, gitignore, and `.references/prompt.md` were inspected. Generated binaries were exercised and their build metadata inspected rather than treated as source. External scripts referenced by the project have not received a full audit; their behavior remains part of the unfinished integration review.
