# Contribute skills design

Provide a bundled skill that directs agents to contribute skills to the active Capability Pack's source repository. AACT supplies the active pack manifest path to rendered templates; the installed skill reads that manifest, resolves its catalog/import paths, and follows the identified repository's own instructions for layout, checks, and pull requests. It asks for clarification only when the source checkout cannot be established. Preserve existing work and user authorization rules.

## Approaches

Use the active manifest path already known to AACT and existing repository instructions. An alternative is asking each pack to duplicate repository identity and workflow inputs, which is redundant and can drift. The change adds template context to AACT rendering but does not configure consuming packs.
