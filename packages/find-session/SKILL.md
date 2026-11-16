---
name: find-session
description: Use when searching `find-session` or locating prior sessions across Claude, Codex, and OpenCode session stores.
version: 1.0.0
---

# Find Session

Use this skill when you need to search across all available sessions from the local agent stores.

This skill is launcher-first:

- run `bin/find-session` (Unix) or `bin/find-session.exe` (Windows) relative to the directory containing this SKILL.md; use its absolute path, not the shell's current directory
- pass the search term or filter arguments straight through
- let the launcher search the mounted session stores for you

## What it searches

The wrapper mounts these read-only session locations into the container:

- `~/.claude`
- `~/.codex/sessions`
- `~/.local/share/opencode`

Use it to find a previous conversation, recall a past decision, or trace work done in another session.

## Typical usage

```bash
"<absolute-skill-directory>/bin/find-session" -g "<query>"
```

Examples:

```bash
"<absolute-skill-directory>/bin/find-session" -g postgres
"<absolute-skill-directory>/bin/find-session" -g "migration error"
```

Use `-g` for the global, cross-project search. That is the default path when you are trying to find an old session anywhere in the local stores.

On Windows, invoke `bin\find-session.exe` under the directory containing this
skill. In PowerShell, use `& 'C:\absolute\skill\directory\bin\find-session.exe' -g 'postgres'`,
replacing the example directory with the actual skill directory. In WSL, use
the Linux `bin/find-session` from the skill installed in that WSL distribution;
it searches the WSL user's local history directories. The Windows `.exe` uses
the Windows user's history directories when launched from PowerShell. Docker
must be able to bind-mount the WSL home; Docker Desktop's WSL integration
supports this, while Docker VMM does not mount paths under `/home`.

## Notes

- Release launchers run the matching pre-published GHCR image; development builds without a release image version build locally.
- It adds Docker interactive/TTY flags only for an interactive terminal; noninteractive searches work without them.
- Missing agent-history directories are skipped. The launcher requests read-only history mounts; a compatible runtime may expose a staged copy as writable.
- POSIX history paths containing a colon use Docker's `--mount` syntax to keep the source unambiguous. Some compatible runtimes support only `-v`, so those unusual paths may not work there. A POSIX path containing both a colon and comma is rejected because neither Docker syntax can represent it safely.
- Do not reimplement session search logic in the agent when this wrapper already exists.
