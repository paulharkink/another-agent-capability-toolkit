---
name: git-repo-map
description: Use when resolving a remote Git repository name to a local checkout, locating another repository on this machine, or choosing between multiple local checkouts.
---

# Local Git Repository Map

This inventory maps network Git origin remotes to local working checkouts.
Consult it before assuming a repository's directory. Match both the host and
repository name; names can be identical on different servers. Repository names
retain their original case.

## Local checkouts

| Host | Repository | Local path |
| --- | --- | --- |
| `git.example` | `team/widget` | `/work/research & development/<widget>` |

## Using the map

- Use the host and repository name together, such as `git.example` and
  `team/widget`, to identify a repository.
- When several paths match, inspect their branch and working changes before
  choosing one. Preserve existing changes and follow that checkout's AGENTS.md.
- Check that the path still exists before using it. A missing entry means the
  checkout was not found within the selected roots at generation time; it does
  not establish whether the repository exists on its server.
- The inventory includes linked worktrees and repositories with an `origin`
  network remote. Local filesystem remotes, repositories without `origin`, bare
  repositories, and symlinked directories within roots are excluded.
- Hostnames are lowercase. HTTPS and SSH default ports are omitted; other ports
  remain part of host identity. Different hostnames are never assumed to be aliases.

## Refreshing and scan scope

Regenerate this skill through the manager after cloning, moving, deleting, or
changing the origin of a checkout. Review any scan diagnostics: inaccessible
roots or unreadable repository metadata fail generation rather than publishing
an apparently complete inventory.

Selected scan roots:

- `/work/research & development`

All network hosts are included.

Dependency and cache directories such as `node_modules`, `vendor`, `.cache`, `.local/share`,
`.npm`, `.cargo`, `.rustup`, `.gradle`, `.venv`, and `venv` are pruned. Choose
project roots to keep the inventory focused. An explicitly selected root is
scanned even if its own name normally belongs to a pruned directory.

The bundled native generator reads filesystem metadata using go-git. It does
not contact remote servers and requires no Git, find, shell, Python, or Go
installation to run. Generator progress appears on stderr; stdout contains the
JSON inventory consumed by this template.
