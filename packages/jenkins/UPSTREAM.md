# Upstream Jenkins MCP

The container installs the unmodified `jenkins-mcp` npm package, pinned to
version `0.2.1`, from [mcpland/jenkins-mcp](https://github.com/mcpland/jenkins-mcp).
The upstream project is MIT licensed. Its package and dependency license files
are included in the built image by npm.

AACT adds only the Docker packaging and `entrypoint.sh` that selects the
upstream `--read-only` option from the capability input and starts the server
with Streamable HTTP enabled. The Jenkins MCP server implementation is not
forked or modified.
