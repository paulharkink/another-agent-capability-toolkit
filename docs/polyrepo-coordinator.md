# Polyrepo Coordinator

Polyrepo Coordinator keeps its repository inventory and peer descriptors on the
machine. It does not require a peer listener for a single-node setup.

Configure the local search boundary and, when creating managed worktrees, an
explicit worktree directory:

```sh
aact --state-dir /private/aact-state polyrepo configure \
  --root /work/repos --worktree-root /work/courier-worktrees
aact --state-dir /private/aact-state polyrepo list
```

The inventory is written to
`<state-root>/manager/repo-map-v1.json`. It contains a stable per-node UUID,
repository host/name/path, a hash of each checkout's canonical Git common
directory, worktree metadata, configured peer descriptors, and the optional
explicit `worktreeRoot`. Linked worktrees share `repoId`; separate clones do
not. Only local Git metadata is read; origin URLs are never contacted.

To add a peer, create a private key file outside any portable Capability Pack
and configure only its reference:

```sh
aact --state-dir /private/aact-state polyrepo peer add \
  --peer-id windows-node --peer-url https://node.example.invalid \
  --secret-file /private/keys/courier-peer.key \
  --allowed-root /work/repos
```

The key file must be a regular file inaccessible to group/other users. AACT
stores its path in protected local manager settings; key contents are never
read or copied into the inventory or skill.

`polyrepo apply` builds the pinned Courier fork from source below AACT's
manager state and registers the generated local entrypoint through the generic
OpenCode plugin manager. For development, an explicit local checkout can be
provided:

```sh
aact --state-dir /private/aact-state --agent-home opencode=/private/opencode-home \
  polyrepo apply --opencode-config /private/opencode-home/opencode.jsonc \
  --courier-source /work/opencode-courier
```

The package build happens on a private copy. Successful builds are retained by
revision under `manager/plugins/polyrepo-coordinator/builds/`; a build failure
does not replace the currently registered plugin. The V2 OpenCode plugin entry
contains only the local entrypoint and `repoMapFile` option. OpenCode provider,
MCP and unrelated plugin settings are preserved.
