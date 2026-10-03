# AACT interactive TUI design mock

This static page demonstrates the intended Mac experience for discussion and
review. It uses illustrative `agent-skills`, macOS, and `home / pms15` data. It
does not read or change local state, agent configuration, credentials, or Docker.

Open `index.html` through any static HTTP server. Its CSS and JavaScript are
local files, so the directory can later be served directly from GitHub Pages
with a `/docs` Pages source. Opening the HTML via `file://` may block ES modules
in some browsers.

Run the design checks with `node --test test/demo-*.test.mjs` from the repo root.
Node is only needed for these mock tests; it is not an AACT runtime dependency.
