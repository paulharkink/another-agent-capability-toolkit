---
name: jenkins
description: Inspect Jenkins jobs, builds, logs, test reports, nodes, and queues through the Jenkins MCP server; trigger a job only when the user explicitly requests it and write tools are enabled.
---

# Jenkins

Use the Jenkins MCP server to find jobs, inspect build status, and read bounded
console output. For failed builds, start with `get_build`, then use
`get_build_failure_excerpt` or `search_build_console`; fetch incremental chunks
only when more context is needed. Avoid requesting an entire console log when a
tail, search, or excerpt answers the question.

The capability's `read_only` input defaults to `true`. In that mode the server
does not expose write tools, including the job trigger. If the user asks to start
a job and the tool is unavailable because read-only mode is enabled, explain
that the capability must be reconfigured with `read_only = false`.

With read-only mode disabled, the upstream server also exposes Jenkins
configuration and cancellation tools in addition to `build_item`. Only use a
write tool when the user explicitly asks for that specific change. Never modify
job, node, or queue configuration as a side effect of inspecting a build.
