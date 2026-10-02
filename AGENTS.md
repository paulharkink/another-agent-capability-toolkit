# AACT repository rules

- Follow the approved plan in docs/superpowers/plans/2026-09-30-aact.md.
- Use TDD: record the intended failing test before implementation, then verify green.
- All product changes go through pull requests. Never push directly to main.
- Do not use Computer Use.
- Work only in your assigned directories; the coordinator owns go.mod, go.sum and Git commits during parallel work.
- No live user agent configuration, credentials or existing MCP instances may be modified during build/test.
- External service failures do not stop independent tasks; mocks must be labeled and real verification failures reported honestly.
