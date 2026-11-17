# Contribute skills design

Provide a bundled skill that directs agents to contribute skills to the active Capability Pack's source repository. AACT resolves the Git checkout from the active pack manifest path during rendering and supplies that repository path to the skill. The skill follows the repository's own instructions for layout, checks, and pull requests. It asks for clarification only when the checkout or contribution instructions are missing. Preserve existing work and user authorization rules.

## Approaches

Derive the nearest Git root from the active manifest path in the renderer and pass that root to templates. If Git metadata is absent, use the manifest directory. An alternative is asking each pack to duplicate repository identity and workflow inputs, which is redundant and can drift. The change adds template context to AACT rendering but does not configure consuming packs.
