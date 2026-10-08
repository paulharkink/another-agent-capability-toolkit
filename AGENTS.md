# AACT repository rules

- Follow the approved plan in docs/superpowers/plans/2026-10-08-capability-packs-profiles-adapters.md. The user requires implementation and tool execution through Luna subagents; use the approved subagent-driven-development flow with TDD and independent review gates. The primary controller coordinates only and must not use repository or terminal tools.
- Use TDD: record the intended failing test before implementation, then verify green.
- All product changes go through pull requests. Never push directly to main.
- Do not use Computer Use.
- Work only in the assigned checkout. Luna subagents own implementation and all repository/terminal tool use, including dependency files and local task commits; the controller coordinates only.
- No live user agent configuration, credentials or existing MCP instances may be modified during build/test.
- External service failures do not stop independent tasks; mocks must be labeled and real verification failures reported honestly.
