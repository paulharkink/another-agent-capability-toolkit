# AACT interactive TUI design mock

This static page demonstrates the intended Mac experience for discussion and
review. It uses illustrative `example-company`, macOS, and `ota` profile data. It
does not read or change local state, agent configuration, credentials, or Docker.

Open `index.html` through any static HTTP server. Its CSS and JavaScript are
local files, so the directory can later be served directly from GitHub Pages
with a `/docs` Pages source. Opening the HTML via `file://` may block ES modules
in some browsers.

On the home screen, Enter on a capability focuses its profile rows on the right.
Enter on a profile opens the configuration; Create another profile asks for a
local name. Esc returns one layer and F10 exits the real TUI. All values/statuses
in this page are labeled sample data.

In a setup popup, Tab and Shift-Tab cycle sections, details, and the action
bar. Up/Down moves through controls within an area. Left returns from details
to sections when a text cursor is at the start of its field. Ctrl-S or Cmd-S
activates Save and apply. Esc returns from details to sections, then cancels
from sections, with a discard prompt for unsaved edits.

Run the design checks with `node --test test/demo-*.test.mjs` from the repo root.
Node is only needed for these mock tests; it is not an AACT runtime dependency.

## Capability Pack/profile revision

The home right pane begins with configuration profiles (including profiles for
skill-only capabilities), followed by Create another profile and read-only
capability information. Enter on the left focuses the right; Enter on a profile
opens its configuration. Creation records only simulated local state.

Components is a keyboard checklist of independent skills and linked skill/MCP
sets. Select all skills does not enable native plugins or change MCP enable
inputs. The Guidance bundle demonstrates two skills, a Boolean checkbox and an
optional Claude plugin. Unsupported plugins remain unavailable until a compatible
agent is selected. A single bottom Save and apply handles the selected capability.

This is a static, illustrative reference. Real terminal captures and verification
are recorded separately; passing browser tests does not prove real TUI fidelity.
