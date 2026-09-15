# `agent-cli` (`ai`) — Universal Desktop AI Agent Manager

A modular, high-performance command-line interface written in Go (modeled structurally after [`steam-cli`](https://github.com/Bluscream/steam-cli)) for monitoring, auditing, configuring, and cross-collaborating across all desktop AI agent providers on Linux:
- **Claude Desktop** (Debian AppImage via [`aaddrick/claude-desktop-linux`](https://github.com/aaddrick/claude-desktop-linux)) & Claude Code
- **Antigravity IDE** (Linux Homebrew tap `ublue-os/tap/antigravity-ide-linux`) & CLI
- **Codex Desktop** (Community Linux distribution via [`ilysenko/codex-desktop-linux`](https://github.com/ilysenko/codex-desktop-linux) packaging OpenAI's official deb)

---

## Key Feature: Zero-Digging Cross-Agent Audits

Tell any agent:
> *"Use the `ai audit --provider claude --last` command to look up everything you need from my last conversation with Claude Desktop and finish off its work for me."*

With a single call (e.g. `ai audit --provider claude --last --output json`), the agent receives a complete, structured briefing containing:
1. Conversation title, ID, timestamps
2. Associated workspace path & branch
3. Real-time Git status (modified files, staged files, uncommitted diff stat, latest commits)
4. Initial user goal & prompt
5. Last agent messages, proposed next steps, and open action items
6. Touched/created artifacts

---

## Agent Discovery & System Locations

| Agent | Binary / Launcher | Origin & Packaging | Config & State | Transcripts & Data |
| :--- | :--- | :--- | :--- | :--- |
| **Claude Desktop** | `/home/blu/.local/bin/Claude_Desktop.AppImage` | `io.github.aaddrick.claude-desktop-debian` (GitHub) | `~/.config/Claude/claude_desktop_config.json` | `~/.claude/projects/`, `~/.config/Claude/claude-code-sessions/` |
| **Antigravity IDE** | `/var/home/linuxbrew/.linuxbrew/bin/antigravity-ide` | `ublue-os/tap/antigravity-ide-linux` (Linuxbrew) | `~/.config/Antigravity IDE/User/globalStorage/state.vscdb` | `~/.gemini/antigravity-ide/brain/`, `~/.gemini/antigravity-ide/conversations/` |
| **Codex Desktop** | `/home/blu/Applications/codex-desktop.AppImage` | `ilysenko/codex-desktop-linux` (OpenAI deb repackager) | `~/.codex/.codex-global-state.json`, `~/.codex/state_5.sqlite` | `~/.codex/sessions/**/*.jsonl`, `~/.codex/memories_1.sqlite` |

---

## Command Reference

### Global Flags
- `--output, -o`: Output format (`auto`, `table`, `json`, `compact`, `csv`)
- `--provider, -p`: Filter by provider (`antigravity`, `claude`, `codex`)
- `--color`: Colour output control (`auto`, `always`, `never`)
- `--with-header`: Include/suppress tabular/CSV headers (default: `true`)
- `--last`: Target only the most recent conversation
- `--max-column-length`: Maximum column width before wrapping in tables (default: `100`, `-1` for uncapped)
- `--debug`: Enable execution step timing and latency breakdown

### 1. `ai status`
Displays installed status, running state, active task activity, PID(s), CPU%, Memory (RSS), and configured MCP server counts.

```bash
ai status
ai status --provider claude
ai status --output json
```

### 2. `ai history`
Lists conversation sessions sorted chronologically across all agents with responsive auto-fit column wrapping.

```bash
ai history
ai history --limit 10
ai history --provider codex
ai history --workspace /some/dir # Filter to conversations in a workspace or parent
ai history --since 2w            # Conversations in the last 2 weeks
ai history --since "1 day"       # Conversations in the last day
ai history --last                # Only the single most recent session
ai history --output json
```

### 3. `ai search`
Universal cross-entity search across conversations, transcripts, memories, and skills with attribution (who, when, where) and context snippets.

```bash
ai search "pkg-manager"
ai search --text "steam-cli" --workspace "/run/media/system/Data/Projects"
ai search --pattern "git\s+(commit|push)"
ai search --type memories "api key"
ai search "pkg-manager" --output json
```

### 4. `ai conversation <id>`
Inspects granular details about a conversation:
- Timestamps, model, workspace directories
- Live Git status in the workspace (branch, commit, dirty status, diff stat)
- Catalog of all generated brain artifacts
- Recent turn previews and tool calls
- `--recover`: Scans for unindexed or corrupted conversations and restores them to `state.vscdb`

```bash
ai conversation 01a09c4e-8105-7693-acbc-8d19858fa198
ai conversation --recover --dry-run
ai conversation --recover
```

### 5. `ai lasts`
Displays rich, multi-field briefing cards for the most recent N conversations across providers, including initial user prompts and last agent responses.

```bash
ai lasts
ai lasts -n 5
ai lasts --provider claude
```

### 6. `ai log <id>`
Prints turn-by-turn conversation transcripts with tool calls and internal reasoning.

```bash
ai log 046f0687
ai log 046f0687 --tools
ai log 046f0687 --thinking
ai log 046f0687 --system
ai log --last --no-errors
```

### 7. `ai id <identifier>`
Generic lookup tool that resolves any short ID or full raw ID to the underlying entity (conversation, memory item, skill, or MCP server).

```bash
ai id 046f0687
ai id ba07b876
ai id omni-mcp
```

### 8. `ai handoff [<id>]` (alias: `ai audit`)
Produces a self-contained briefing dossier of a conversation for cross-agent auditing and handoffs.

```bash
ai handoff --provider claude --last
ai handoff --provider codex --last --output json
ai handoff 98256169-57f7-4239-9eaf-c73532e3253d
```

### 9. `ai memory`
Inspects, backs up, or purges agent memories and knowledge stores across providers.

```bash
ai memory
ai memory --backup                      # Backs up to ~/.local/share/agent-cli/backups/
ai memory --backup --backup-dir /tmp/   # Custom backup path
ai memory --purge                       # Purges saved memory items
```

### 10. `ai skill`
Lists and inspects skills (both builtin and custom) across Antigravity, Codex, and Claude.

```bash
ai skill
ai skill --provider antigravity
ai skill --output json
```

### 11. `ai mcp`
Centralized MCP (Model Context Protocol) server manager that synchronizes and configures servers across all IDEs and tools on your system (ports and improves `manage-mcp-servers.sh`).

Supported config targets:
- `~/.config/Antigravity IDE/User/settings.json`
- `~/.gemini/antigravity-ide/mcp_config.json`
- `~/.gemini/config/mcp_config.json`
- `~/.config/Claude/claude_desktop_config.json`
- `~/.config/VSCodium/User/globalStorage/zoocodeorganization.zoo-code/settings/mcp_settings.json`
- `/var/mnt/nas/projects/MCPs/mcp.json`

```bash
ai mcp list
ai mcp sync
ai mcp add --name my-server --command node --args index.js
ai mcp add --name remote-server --url http://127.0.0.1:8000/mcp --token secret
ai mcp enable my-server
ai mcp disable my-server
ai mcp remove my-server
```

### 12. `ai plugins`
Manages universal plugin injection and helper services:
- **Claude ASAR Patcher**: Injects universal plugin loader hook into Claude Desktop AppImage's `app.asar` (pointing to `~/.config/Claude/plugins/loader.js`).
- **Claude Auto-Nudge**: Controls the background OCR/pointer auto-nudge macro.
- **Extensions Catalog**: Lists plugins in `~/.codex/plugins` and `~/.gemini/config/plugins`.

```bash
ai plugins status
ai plugins patch
ai plugins restore
ai plugins nudge status
ai plugins nudge start
ai plugins nudge stop
```

### 13. `ai account`
Multi-profile account switcher for Antigravity IDE and agents (ports `antigravity-switcher.sh`).

```bash
ai account list
ai account save work-profile    # Captures active session tokens & updates desktop shortcut
ai account switch work-profile  # Gracefully restarts IDE with selected profile
ai account fresh                # Clears active session keys for a fresh login test
```

### 14. `ai models`
Displays models available from each installed AI provider with active model indicators and context window limits.

```bash
ai models
ai models --descriptions        # Include detailed model descriptions
ai models --provider codex
ai models --output json
```

### 15. `ai limits`
Shows account usage limits, quotas, rate limits, and reset times across all providers from local state without network calls.

```bash
ai limits
ai limits --provider codex
ai limits --output json
```

---

---

## Debug Timing & Performance Profiling

`ai` contains a zero-overhead step-by-step performance timer designed to track latency across providers, filesystem scanners, and SQLite queries.

### Usage:
1. **Debug Build**:
   ```bash
   make build-debug     # Builds bin/ai-debug with -tags debug
   ./bin/ai-debug status
   ```
   Debug builds automatically render timing summaries in human-readable table format, and embed a structured `_debug` timing breakdown into JSON output:
   ```json
   {
     "_debug": {
       "build_mode": "debug",
       "total_duration": "43.2ms",
       "steps": [
         {"step": "startup_flags_parsed", "duration": "143ns"},
         {"step": "provider_resolved", "duration": "3.5µs"},
         {"step": "conversation_audited", "duration": "43.1ms"}
       ]
     },
     "data": { ... }
   }
   ```
2. **Release Build with Debug Flag or Environment Variable**:
   ```bash
   ai status --debug
   AI_DEBUG=1 ai history --limit 5
   ```

---

## Build, validate, and deploy

```bash
scripts/build.sh             # Full validation; writes bin/ai and bin/ai-debug
scripts/build.sh --deploy    # Same gates, then atomically installs ~/.local/bin/ai
```

Run this after changes. It checks formatting, module checksums, vet, pinned
Staticcheck, race and debug tests, release/debug compilation, vulnerabilities in
the actual release binary, and isolated CLI command/format regressions. Test
executables run on the host so a containerized Go launcher cannot silently skip
SQLite integration tests. Skipped tests fail the build.

Requires Bash, Go, Git, Python 3, SQLite 3, `timeout`, and `install`. The default
Go toolchain is pinned to `go1.26.8`; first use may download it and analysis tools.
Use `BINDIR` to choose the install directory and `VERSION` to override the Git
revision label. Logs and `summary.txt` are under `bin/build-logs/`. Failed checks
leave the installed executable untouched. These automated gates complement the
ongoing review; they cannot prove that every provider behavior is correct.

## Development & Meta Helpers

The repository includes meta validation targets to enforce idiomatic formatting, vetting, testing, and multi-mode builds:

```bash
make fmt          # Format all Go source files (go fmt ./...)
make fmt-check    # Check formatting without changing files
make lint         # Run Go static analysis (go vet ./...)
make staticcheck  # Run pinned, deeper static analysis (downloads tool if needed)
make test         # Run unit tests across all packages
make build        # Compile release binary to bin/ai
make build-debug  # Compile debug-instrumented binary to bin/ai-debug
make check        # Check formatting, vet, test, and build release/debug binaries
make install      # Install release binary to ~/.local/bin/ai
make clean        # Remove compiled binaries
```


## Audit and compatibility status

An incremental audit and repair is in progress. See
[the audit log](docs/audits/2026-09-15/README.md) for completed repairs,
verification, and outstanding limitations. Some advertised operations still
need compatibility work; in particular, memory/skill synchronization, plugin
registration, and recovery should not be treated as fully validated integrations.

Runtime dependencies include `sqlite3` (with JSON output support), `git`, and
Linux `/proc`. Claude patching and auto-nudge also depend on external scripts
at the paths reported by `ai plugins status`. The Go build does not bundle
these dependencies.

MCP edits reject malformed JSON and preserve unknown server fields. Updates
replace each file atomically, but changes across multiple files are not a
single transaction. JSON-with-comments configurations are not yet supported.
Account save/switch/fresh currently support Antigravity only and reject other
provider selections.
