# Project vocabulary

Use these terms consistently in user-facing documentation, designs, and code
comments. The distinctions below reflect the agreed AACT model.

## Capability

A reusable catalog definition that AACT can install or configure. A catalog
entry identifies a Capability; it is not one configured copy of that
Capability.

## Capability Instance

A configured, individually managed instance of one Capability. An instance
holds that Capability's configuration. It can be a member of zero, one, or
multiple Profiles.

## Profile

A separately managed grouping of Capability Instances. A Profile can group
instances from different Capabilities. Membership refers to existing
instances; it does not copy their configuration or credentials.

Capability Packs may supply initial Profiles and memberships. Users may also
create Profiles and manage their memberships.

## Capability Pack

A project-provided catalog and configuration source for AACT. A Pack can supply
Capability definitions and initial Profile groupings.

## Environment

Environment is not the term for a grouping of Capability Instances. Do not use
it as a synonym for Profile or as a separate selectable grouping in the agreed
model. Older documentation and configuration interfaces may still use
“environment” for target configuration; make that legacy or configuration
context explicit when referring to it.

## Historical use of “Profile”

Some older AACT design documents and compatibility names use “profile” for a
single Capability's configuration. That usage predates the agreed distinction:
use **Capability Instance** for one configured Capability, and **Profile** for
a grouping of instances. When changing a legacy schema, command, or stored
field, preserve compatibility unless a separate change explicitly addresses
the migration.
