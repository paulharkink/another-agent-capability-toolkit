---
name: forgejo
description: Inspect repositories, issues, pull requests, releases, wiki pages, and CI on the selected Forgejo target.
version: 1.0.0
---

# Forgejo

Use this MCP to inspect repositories, issues, pull requests, releases, wiki pages, and CI information on the selected Forgejo target.

Discover Forgejo MCP targets registered in the current client at runtime. Match an explicitly named target; otherwise select the sole applicable target automatically. If several could apply, ask which one to inspect. If none are registered, report that no Forgejo target is available. Do not keep a saved target list. This applies to local clients and in-cluster agents.

## Write operations

The Forgejo API token may allow changes. Before creating, editing, merging, closing, deleting, or triggering anything, summarize the exact change and get explicit user confirmation. Prefer read-only inspection when the request does not require a change. Never reveal or persist the token outside its target-specific state directory.
