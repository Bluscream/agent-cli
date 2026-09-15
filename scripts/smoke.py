#!/usr/bin/env python3
"""Exercise built CLIs against temporary data; never mutate provider state."""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile


def check(binary, home):
    env = dict(os.environ, HOME=str(home), AI_DEBUG="0", DEBUG="0", NO_COLOR="1")
    env.pop("COLUMNS", None)

    def invoke(*args, structured=True, success=True):
        result = subprocess.run(
            [str(binary), *args], env=env, capture_output=True, text=True, timeout=30
        )
        if (result.returncode == 0) != success:
            raise AssertionError(f"{args}: unexpected exit {result.returncode}: {result.stderr}")
        if not success:
            return None
        if not structured:
            if not result.stdout.strip():
                raise AssertionError(f"{args}: empty output")
            return None
        try:
            value = json.loads(result.stdout)
        except ValueError as error:
            raise AssertionError(f"{args}: expected one JSON document") from error
        return value.get("data") if isinstance(value, dict) and "_debug" in value else value

    commands = [
        ["status"], ["history"], ["lasts", "--limit", "1"],
        ["models"], ["limits"], ["memory"], ["skill"],
        ["account"], ["account", "list"], ["mcp"], ["mcp", "list"],
        ["plugins"], ["plugins", "list"], ["plugins", "status"],
        ["plugins", "nudge", "status"],
    ]
    for args in commands:
        invoke(*args, "--output=compact")
    rows = invoke("history", "--provider=codex", "--output=compact")
    assert len(rows) == 2 and any(row["title"] == "multi\nline" for row in rows)
    rows = invoke("history", "--provider=codex", "--workspace=/target", "--limit=1", "--output=compact")
    assert len(rows) == 1 and rows[0]["workspace_dir"] == "/target"
    memories = invoke("memory", "--provider=codex", "--output=compact")
    assert len(memories) == 1 and memories[0]["content"] == "line1\nline2"
    for command in ["conversation", "id", "handoff", "audit"]:
        invoke(command, "claude-test-id", "--output=compact")
    log = invoke("log", "--last", "--provider=claude", "--output=compact")
    assert log["provider"] == "claude" and len(log["turns"][0]["content"]) > 600
    for duration in ["1 minute", "2026-09-01T00:00:00Z"]:
        invoke("history", "--since", duration, "--output=compact")
    for command in ["models", "limits"]:
        invoke(command, "--provider=nonexistent", "--output=compact", success=False)
    invoke("--help", structured=False)
    invoke("--version", structured=False)
    for shell in ["bash", "zsh", "fish", "powershell"]:
        invoke("completion", shell, structured=False)
    for output in ["auto", "table", "csv", "json", "compact"]:
        invoke("history", "--provider=codex", f"--output={output}", structured=output in ["json", "compact"])
    print(f"PASS: {binary.name}: read commands, formats, provider selection, multiline records, full content, errors, completion")


def main():
    if len(sys.argv) != 3:
        raise SystemExit("Usage: smoke.py RELEASE_BINARY DEBUG_BINARY")
    with tempfile.TemporaryDirectory(prefix="ai-build-smoke-") as directory:
        home = Path(directory)
        codex = home / ".codex"
        codex.mkdir()
        with sqlite3.connect(codex / "state_5.sqlite") as db:
            db.execute("CREATE TABLE threads(id,title,created_at,updated_at,cwd,model,rollout_path)")
            db.executemany("INSERT INTO threads VALUES(?,?,?,?,?,?,?)", [
                ("codex-new-id", "new", 1, 200, "/elsewhere", "test", ""),
                ("codex-old-id", "multi\nline", 1, 100, "/target", "test", ""),
            ])
        with sqlite3.connect(codex / "memories_1.sqlite") as db:
            db.execute("CREATE TABLE stage1_outputs(thread_id,raw_memory,rollout_summary,rollout_slug,generated_at)")
            db.execute("INSERT INTO stage1_outputs VALUES(?,?,?,?,?)", ("memory-id", "line1\nline2", "summary", "slug", 1))
        claude = home / ".claude/projects/-target"
        claude.mkdir(parents=True)
        (claude / "claude-test-id.jsonl").write_text(json.dumps({
            "type": "user", "timestamp": "2026-09-15T00:00:00Z",
            "message": {"role": "user", "content": "A long instruction. " * 100},
        }) + "\n")
        for path in sys.argv[1:]:
            check(Path(path).resolve(), home)


if __name__ == "__main__":
    main()
