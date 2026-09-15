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

### 1. `ai status`
Displays installed status, running state, active task activity, PID(s), CPU%, Memory (RSS), and configured MCP server counts.

```bash
ai status
ai status --provider claude
ai status --output json
```

### 2. `ai history`
Lists conversation sessions sorted chronologically across all agents.

```bash
ai history
ai history --limit 10
ai history --provider codex
ai history --since 2w       # Conversations in the last 2 weeks
ai history --since "1 day"  # Conversations in the last day
ai history --last           # Only the single most recent session
ai history --output json
```

### 3. `ai conversation <id>`
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

### 4. `ai audit [<id>]`
Produces a self-contained briefing dossier for cross-agent collaboration and handoffs.

```bash
ai audit --provider claude --last
ai audit --provider codex --last --output json
ai audit 98256169-57f7-4239-9eaf-c73532e3253d
```

### 5. `ai memory`
Inspects, backs up, or purges agent memories and knowledge stores across providers.

```bash
ai memory
ai memory --backup                      # Backs up to ~/.local/share/agent-cli/backups/
ai memory --backup --backup-dir /tmp/   # Custom backup path
ai memory --purge                       # Purges saved memory items
```

### 6. `ai skill`
Lists and inspects skills (both builtin and custom) across Antigravity, Codex, and Claude.

```bash
ai skill
ai skill --provider antigravity
ai skill --output json
```

### 7. `ai mcp`
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

### 8. `ai plugins`
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

### 9. `ai account`
Multi-profile account switcher for Antigravity IDE and agents (ports `antigravity-switcher.sh`).

```bash
ai account list
ai account save work-profile    # Captures active session tokens & updates desktop shortcut
ai account switch work-profile  # Gracefully restarts IDE with selected profile
ai account fresh                # Clears active session keys for a fresh login test
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

## Development & Meta Helpers

The repository includes meta validation targets to enforce idiomatic formatting, vetting, testing, and multi-mode builds:

```bash
make fmt          # Format all Go source files (go fmt ./...)
make lint         # Run Go static analysis (go vet ./...)
make test         # Run unit tests across all packages
make build        # Compile release binary to bin/ai
make build-debug  # Compile debug-instrumented binary to bin/ai-debug
make check        # Run fmt, lint, test, build, and build-debug in one pass
make install      # Install release binary to ~/.local/bin/ai
make clean        # Remove compiled binaries
```

