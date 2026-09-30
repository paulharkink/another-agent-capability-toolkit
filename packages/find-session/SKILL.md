---
name: find-session
description: Use when searching `find-session` or locating prior sessions across Claude, Codex, and OpenCode session stores.
version: 1.0.0
---

# Find Session

Use this skill when you need to search across all available sessions from the local agent stores.

This skill is launcher-first:

- run the bundled `./bin/find-session` (Unix) or `./bin/find-session.exe` (Windows)
- pass the search term or filter arguments straight through
- let the launcher search the mounted session stores for you

## What it searches

The wrapper mounts these read-only session locations into the container:

- `~/.claude`
- `~/.codex`
- `~/.local/share/opencode`

Use it to find a previous conversation, recall a past decision, or trace work done in another session.

## Typical usage

```bash
./bin/find-session -g <query>
```

Examples:

```bash
./bin/find-session -g postgres
./bin/find-session -g "migration error"
```

Use `-g` for the global, cross-project search. That is the default path when you are trying to find an old session anywhere in the local stores.

## Notes

- The launcher builds its Docker image before running.
- It adds Docker interactive/TTY flags only for an interactive terminal; noninteractive searches work without them.
- Missing agent-history directories are skipped; existing history is mounted read-only.
- Do not reimplement session search logic in the agent when this wrapper already exists.
