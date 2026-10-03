# AACT interactive TUI design mock

This static page demonstrates the intended Mac experience for discussion and
review. It uses illustrative `agent-skills`, macOS, and `home / pms15` data. It
does not read or change local state, agent configuration, credentials, or Docker.

Open `index.html` through any static HTTP server. Its CSS and JavaScript are
local files, so the directory can later be served directly from GitHub Pages
with a `/docs` Pages source. Opening the HTML via `file://` may block ES modules
in some browsers.

On the home screen, Enter on a capability in the left pane moves to that
capability's right-hand menu. Enter on a right-hand row opens its setup,
details, or profile actions. Back returns to the previous layer. In the home
detail pane, Esc returns focus to the capability list; F10 is Quit. In the
registration overlay, select `Endpoint URI` on the left to see the endpoint
and Check connection; selecting an agent shows only that agent's settings.

In a setup popup, Tab and Shift-Tab cycle sections, details, and the action
bar. Up/Down moves through controls within an area. Left returns from details
to sections when a text cursor is at the start of its field. Ctrl-S or Cmd-S
activates Save or Install. Esc returns from details to sections, then cancels
from sections, with a discard prompt for unsaved edits.

Run the design checks with `node --test test/demo-*.test.mjs` from the repo root.
Node is only needed for these mock tests; it is not an AACT runtime dependency.
