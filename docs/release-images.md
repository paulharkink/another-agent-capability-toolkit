# Release container images

Tagged AACT releases publish the `find-session` runtime as a multi-platform
image at
`ghcr.io/<repository-owner>/another-agent-capability-toolkit/aact-find-session:v<version>`.
The owner is injected from the GitHub repository used by the release workflow,
and the release binary embeds the same owner and image version. The image is
built for linux/amd64 and linux/arm64 with the MCP images, verified public, and
its immutable digest is recorded in the existing
`mcp-image-digests-<tag>` workflow artifact before publishing GitHub release
assets.

Assets-only recovery runs verify the existing versioned find-session image
before publishing release assets. Development builds with no published image
version continue to build the container locally from
`packages/find-session/container`. The launcher mounts the agent session stores
read-only in either mode.
