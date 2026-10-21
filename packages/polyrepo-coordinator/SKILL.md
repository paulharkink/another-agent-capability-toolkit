# Polyrepo Coordinator

Use the Polyrepo Coordinator OpenCode plugin to inspect local or explicitly
configured peer repositories and manage named Courier work. Before assigning a
managed task, obtain the user's repository, branch, and checkout choices unless
the prompt explicitly grants autonomy for those decisions. Do not infer peer
identity from hostnames or choose a branch/worktree implicitly.

The plugin reads its node-local inventory from the `repoMapFile` option. AACT
stores that inventory and peer descriptors under its local manager state; do
not copy peer credentials or settings into portable skills.
