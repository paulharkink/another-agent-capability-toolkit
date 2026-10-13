"""Minimal Argo CD MCP server entrypoint."""

import sys

from fastmcp import FastMCP

mcp = FastMCP("argocd")


def main() -> None:
    if len(sys.argv) > 1 and sys.argv[1] == "action":
        action = sys.argv[2] if len(sys.argv) > 2 else "unknown"
        raise SystemExit(
            f"Argo CD authentication action {action!r} is unavailable in this build"
        )
    mcp.run(transport="http", host="0.0.0.0", port=8765, path="/mcp")


if __name__ == "__main__":
    main()
