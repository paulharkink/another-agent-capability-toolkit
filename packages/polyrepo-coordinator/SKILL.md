# Polyrepo Coordinator

Use the Polyrepo Coordinator OpenCode plugin to inspect local or explicitly
configured peer repositories and manage named Courier work. Before assigning a
managed task, obtain the user's repository, branch, and checkout choices unless
the prompt explicitly grants autonomy for those decisions. Do not infer peer
identity from hostnames or choose a branch/worktree implicitly.

The plugin reads its node-local inventory from the `repoMapFile` option. AACT
stores that inventory and peer descriptors under its local manager state; do
not copy peer credentials or settings into portable skills.

## Courier coordination

The Head Unit owns cross-repository coordination. Workers report to the Head;
in repo-map mode, a worker must ask the Head to dispatch sibling or follow-up
work instead of spawning children itself.

Group tasks only when they are truly independent: each task must be able to
finish without another task's report, a decision or relay from the Head, or a
follow-up dispatch. A group holds completed, partial, and failed reports until
every member reports. A blocked report is delivered immediately, but grouping
dependent work can keep a completed result from reaching the worker that needs
it. Keep dependent and phased stages ungrouped so the Head can relay results,
answer questions, and dispatch the next stage as soon as its prerequisite is
ready.
