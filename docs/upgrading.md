# Upgrading AACT

## Before upgrading from the legacy generic MCP adapter

Older AACT builds could create generic manual MCP registration records. The
current generic adapter installs skills only, so it cannot remove those older
records. Remove them with the older AACT build **before** upgrading:

1. Start the older build using the same AACT state directory.
2. Open **F9 → Agents**, select each generic MCP registration, and choose
   **Remove**. Confirm that the saved URL is the registration you intend to
   remove.
3. Upgrade AACT after the old registrations have been removed.

If you already upgraded, retrieve the previous AACT build for the version you
were running from its official release archive. If that build was a snapshot,
use the CI artifact for the commit that built it. Do not use the current build
for this cleanup. Reinstall the previous build, point it at the same state
directory (use `--state-dir PATH` if needed), remove each generic registration
as above, then upgrade again. Do not edit AACT's state files or replace whole
configuration files manually.

The old remover checks that the saved registration still matches the artifact
it created. If it reports that the artifact changed or belongs to something
else, stop and leave it untouched; do not force removal or overwrite it.

AACT's generic records were manual configuration artifacts under its state
directory; AACT did not add them to an agent's own configuration. If you
separately copied a generated MCP entry into an agent's configuration, remove
that copy through the agent's own settings.
