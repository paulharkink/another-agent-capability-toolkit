# Git repository map generator

Configure `scan_roots` as one or more directory paths. Set `hosts` to a list of
case-insensitive canonical hostnames (include non-default ports) or omit it to
include all network origins. HTTPS and SSH spellings of the same hostname map
to the same host. Distinct hostnames are not inferred to be aliases.

The manager sends the JSON request to the bundled native `bin/repo-map` helper
(`bin/repo-map.exe` on Windows) and uses the returned object as `generated` in
`SKILL.md.mustache`. `request.json` and `response.json` show synthetic examples.
Scan roots must be real local directories; the example paths are placeholders.
The helper emits exactly one JSON object on stdout and diagnostics on stderr.
A failed or inaccessible root returns a nonzero exit and no partial JSON result.
Invalid Git metadata found below a scan root is silently skipped, so stale
worktrees and copied archive contents do not block mapping valid checkouts.

Paths are absolute and rows are ordered by host, repository name, then path.
Each distinct checkout is retained, even if it shares a remote with another
checkout. Linked Git worktrees and `.git` files are supported. Repositories
without a network `origin`, bare repositories, dependency/cache subtrees, and
symlinked directories below a root are excluded. An explicitly chosen root is
scanned even when its name normally belongs to a pruned directory.

The native helper reads Git metadata through go-git and never contacts a
server. Release archives ship the helper for each platform, so generation
needs no system Git, find, shell, Python, or Go installation. Build from source
with the repository's pinned Go toolchain when developing this package.
