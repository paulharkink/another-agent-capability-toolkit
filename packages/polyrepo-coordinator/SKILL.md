# Polyrepo Coordinator

Use the Polyrepo Coordinator OpenCode plugin to inspect local or explicitly
configured peer repositories and manage named Courier work. Before assigning a
managed task, obtain the user's repository, branch, and checkout choices unless
the prompt explicitly grants autonomy for those decisions. Do not infer peer
identity from hostnames or choose a branch/worktree implicitly.

The plugin reads its node-local inventory from the `repoMapFile` option. AACT
stores that inventory and peer descriptors under its local manager state; do
not copy peer credentials or settings into portable skills.

Before remote worktree creation, confirm the receiving Courier node has a
peer entry for the Head that grants both the selected source checkout and the
receiver's configured `worktreeRoot`. Each `allowedRoots` value is a path on
the node where that peer entry is configured, granting the named peer access
to that local path. Configure reciprocal grants separately on each node and
keep them scoped to intended paths.

AACT may configure this node's optional receiver with a local listener host,
port, and secret-file path before normal capability installation. These
values stay in local AACT settings; never copy secret contents into the pack
or OpenCode configuration.

## Courier coordination

The Head Unit owns cross-repository coordination. Workers report to the Head;
in repo-map mode, a worker must ask the Head to dispatch sibling or follow-up
work instead of spawning children itself.

When work targets a repository managed through the repo map, assign it to a
Courier Worker even when the task is small or sequential. Generic Courier advice
to handle small tasks yourself applies to work in the Head's own checkout; it
does not override this managed-repository workflow. Keep the exact advertised
node ID and full session ID in assignment and follow-up messages (use
`nodeId/sessionID` for a remote session; never substitute a hostname or short
peer label).

While a Worker owns a branch or worktree, send follow-up, review, test, and
verification requests to that same assigned Worker. Do not start another
Worker against its owned checkout. If the assignment has been closed and more
work is needed, inspect the repo map and make a new explicit assignment to the
retained branch/worktree when that is the user's chosen checkout. Closing an
assignment retains its branch and worktree; do not clean them up automatically.

Group tasks only when they are truly independent: each task must be able to
finish without another task's report, a decision or relay from the Head, or a
follow-up dispatch. A group holds completed, partial, and failed reports until
every member reports. A blocked report is delivered immediately, but grouping
dependent work can keep a completed result from reaching the worker that needs
it. Keep dependent and phased stages ungrouped so the Head can relay results,
answer questions, and dispatch the next stage as soon as its prerequisite is
ready.
