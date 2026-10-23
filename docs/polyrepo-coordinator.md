# Polyrepo Coordinator

Polyrepo Coordinator keeps its repository inventory and peer descriptors on the
machine. It does not require a peer listener for a single-node setup.

To enable this node's optional receiver, configure its listener with the local
roots:

```sh
aact --state-dir /private/aact-state polyrepo configure \
  --root /work/repos --worktree-root /work/courier-worktrees \
  --webhook-host 0.0.0.0 --webhook-port 4097 \
  --webhook-secret-file /private/keys/courier-peer.key
```

Listener settings stay in local AACT manager state. The secret file must be a
regular file; on POSIX systems it must not be accessible to group/other users.
On Windows, protect it with the normal user-only file ACL. Secret contents are
never copied into the pack or OpenCode configuration.

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

To let a peer access this node, create a private key file outside any portable
Capability Pack and configure only its reference. `--allowed-root` names a
path on the node where the command runs, granting the named peer access to
that local path. Configure reciprocal peer entries independently on each
node:

```sh
aact --state-dir /private/aact-state polyrepo peer add \
  --peer-id windows-node --peer-url https://node.example.invalid \
  --secret-file /private/keys/courier-peer.key \
  --allowed-root /work/repos/team/project \
  --allowed-root /work/courier-worktrees
```

AACT stores the key path in protected local manager settings; key contents are
never copied into the inventory or skill.

For remote managed worktree creation on node B, configure node B's peer entry
for node A with both B's source checkout and B's configured `worktreeRoot` as
`--allowed-root` values. This grants A permission to use those local paths on
B. Configure the reciprocal entry on A separately, granting B only the
intended paths on A. Listing filters worktrees and destinations by the local
grants, and prepare checks both the selected checkout and explicit destination
against those grants and the local `worktreeRoot`. Missing grants make paths
unavailable; do not broaden a grant implicitly to a shared parent directory.

Install Polyrepo Coordinator as a normal catalog capability. Its declared
plugin build hook checks out pinned Courier revision
`72abe9eaa7af31ee1b44c6f6c80681157ef48788` into the capability's private
staging area, builds it there, and hands the local entrypoint and inventory
path to AACT's generic OpenCode plugin manager. The capability is selected by
default when installing it. For development, pass an explicit local Courier
checkout as the optional `courier_source` input:

```sh
aact --state-dir /private/aact-state install polyrepo-coordinator \
  --config /private/polyrepo-pack/aact.toml --profile local \
  --agent opencode --agent-home opencode=/private/opencode-home \
  --set courier_source=/work/opencode-courier
```

The package build happens on a private copy below AACT's managed generated
state; the supplied checkout is never edited. A build failure leaves the
currently registered plugin intact. Reinstalling builds and atomically updates
the AACT-owned plugin entry; uninstalling removes only that entry and its
managed build directory. The V2 OpenCode plugin entry points at the built
local plugin and includes the local `repoMapFile`; when configured, it also
receives this node's local `webhook` listener settings. OpenCode provider,
MCP, and unrelated plugin settings are preserved. Configure listener settings
before normal `aact install` on each node; do not patch generated OpenCode
JSON by hand.
