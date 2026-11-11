# Atlassian MCP

This package runs Atlassian's official MCP endpoint through a local AACT
container. The container owns the OAuth client flow and keeps its token state
under the AACT-managed `/state` mount.

Choose **Atlassian OAuth** as the default authentication mode and start login
with AACT's explicit **Authenticate** action. The action returns a sign-in URL
for the user to open. Normal health checks and MCP requests do not start a
browser or a new consent flow. OAuth uses the public client metadata document
declared in `client_metadata_url`; that setting can be overridden for isolated
tests.

Applying the package starts and registers the wrapper but does not start OAuth.
Use the explicit **Authenticate** action to begin consent; until then, health
reports the account as unauthenticated.

The legacy `service_account` mode sends a Bearer API token. The `personal` mode
sends the Atlassian account email and API token using HTTP Basic authentication.
Those modes remain available for existing installations; OAuth mode does not
use either input.

The wrapper exposes Streamable HTTP at `/mcp`, a protected-operation health
check at `/health`, the OAuth redirect handler at `/callback`, and its explicit
login action at `/auth/login` on the same container port.
