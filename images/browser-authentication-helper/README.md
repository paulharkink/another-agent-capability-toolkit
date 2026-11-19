# AACT Browser Authentication Helper

The browser-authentication-helper image wraps the verified Kasm Chromium
`1.18.0` image. It runs Chromium in Docker and streams its desktop to a regular
browser through KasmVNC.

Build it once from the repository or extracted AACT release root. AACT uses
this local image and does not build it automatically:

```sh
docker build --pull --tag aact/browser-authentication-helper:1.18.0-form1 images/browser-authentication-helper
```

Start a temporary browser session on the local machine:

```sh
docker run --detach --rm \
  --name browser-authentication-helper \
  --shm-size=512m \
  --publish 127.0.0.1:6901:6901 \
  aact/browser-authentication-helper:1.18.0-form1
```

Open <http://localhost:6901>. No Kasm username or password is required. Stop
the session with:

```sh
docker stop browser-authentication-helper
```

## Generic profile authentication helper

The image includes `browser-authentication-helper run-adapter` for trusted site adapters
started by the AACT daemon. It accepts one bounded JSON request on stdin.
The request selects an origin and either one or more named cookies or
sessionStorage/localStorage keys (exact name, prefix, or all keys at that origin), then
supplies a site adapter's source and non-secret configuration. The adapter is invoked as
`run(credentials, config)`; selected `{key,value}` records are sent to it only
through a private pipe. Adapter source is held in a Linux memfd, not a file.
The helper validates that the adapter returns one bounded JSON object and
forwards that object on stdout. It does not interpret or log credential values
and contains no site-specific authentication logic. Only the trusted host
daemon loads adapter source from the capability; MCP callers cannot provide it.
Cookie lists are selected in one storage read; if a requested name is missing
or ambiguous, the helper fails without invoking the adapter. The legacy single
cookie `name` selector remains accepted alongside the `names` list.

### Reloading a configured site

The daemon can refresh one open page without opening or navigating a
tab by running `browser-authentication-helper reload` and sending one bounded JSON object
on stdin:

```sh
printf '{"origin":"https://app.example"}' \
  | docker exec -i browser-authentication-helper browser-authentication-helper reload
```

When `allowed_urls` is non-empty, the origin must be an explicit, concrete
origin in that policy. With absent or empty `allowed_urls`, the browser policy
is unrestricted and the helper accepts the supplied exact HTTP(S) origin. It
reloads only when exactly one currently open page has that origin, then waits
up to 30 seconds for the page load event. It returns `reloaded`, `not_found`,
`not_configured`, or `ambiguous` with a tab count; `not_configured` and
`ambiguous` issue no reload command. It never
accepts a destination URL or invokes browser navigation. Errors use a
value-free message.

### Selecting a site's credential

The request's `origin` must be the exact site origin. A cookie selector names
one cookie:

```json
{
  "profile_path": "/home/kasm-user/.config/chromium/Default",
  "origin": "https://example.invalid",
  "selector": {"kind": "cookie", "name": "session_id", "match": "exact"},
  "adapter": {
    "source": "def run(credentials, config):\n    return {\"status\": \"selected\" if credentials else \"missing\"}\n",
    "config": {}
  }
}
```

A sessionStorage selector can match one key or a prefix, which is useful for
cache formats whose keys share a known prefix:

```json
{
  "profile_path": "/home/kasm-user/.config/chromium/Default",
  "origin": "https://example.invalid",
  "selector": {"kind": "sessionStorage", "name": "myapp.auth.", "match": "prefix"},
  "adapter": {
    "source": "def run(credentials, config):\n    return {\"status\": \"selected\" if credentials else \"missing\"}\n",
    "config": {"audience": "api://example"}
  }
}
```

The same selector modes work for localStorage:

```json
{
  "profile_path": "/home/kasm-user/.config/chromium/Default",
  "origin": "https://example.invalid",
  "selector": {"kind": "localStorage", "name": "auth.", "match": "prefix"},
  "adapter": {
    "source": "def run(credentials, config):\n    return {\"status\": \"selected\" if credentials else \"missing\"}\n",
    "config": {}
  }
}
```

For an offline profile read that must not contact Chromium or CDP, mount the
profile read-only and run the standalone helper entrypoint. This command
supports cookie, sessionStorage, and localStorage selectors and executes no
Chromium process:

```sh
docker run --rm -i \
  --entrypoint browser-authentication-helper \
  --mount source=aact-browser-profile,target=/home/kasm-user,readonly \
  aact/browser-authentication-helper:1.18.0-form1 run-adapter-offline \
  < request.json
```

The request still includes `profile_path` under the mounted home directory.
The helper refuses offline mode if the profile filesystem is not mounted
read-only. Its stdout contains only the bounded adapter result; extracted
values stay in the helper process and its private adapter pipe.

Some caches, including current MSAL Browser, put the client ID in the cached
record rather than in each key. For those formats, the trusted adapter may
request every key for the exact origin using `"match": "all"` and an empty
`name`; the helper still enforces its record/value/total-size limits. The
adapter must then filter by credential type and client/account metadata before
using any value. This broad selector is intended for in-container adapter
filtering, not to send all site storage to the host or MCP.

For example, a trusted MSAL adapter can select all keys at the site origin and
filter the records in its own code:

```json
{
  "profile_path": "/home/kasm-user/.config/chromium/Default",
  "origin": "https://example.invalid",
  "selector": {"kind": "sessionStorage", "name": "", "match": "all"},
  "adapter": {
    "source": "def run(credentials, config):\n    return {\"status\": \"selected\" if credentials else \"missing\"}\n",
    "config": {"client_id": "example-public-client-id"}
  }
}
```

`adapter.source` must be trusted AACT code that defines `run(credentials,
config)`. The helper passes selected `{key,value}` records to that code through
a private pipe. The adapter should validate the site's credential format and
return only what its caller needs; it must not print or return a refresh token.
`allow_empty` is opt-in for a first-run sign-in flow. Do not place passwords,
cookies, access tokens, or refresh tokens in the request's `config`, command
arguments, logs, or source string.

The Go package's internal `DockerBrowser.ReadBrowserCookies` helper accepts an
arbitrary exact HTTP(S) origin and a list of cookie names. The list is read in
one helper call and one storage snapshot; if any requested cookie is missing or
ambiguous, the call fails without returning partial results. Values return to
the caller in memory. For example, code inside this repository can use:

```go
cookies, err := browser.ReadBrowserCookies(ctx, "https://app.example", []string{
	"session_id",
	"csrf_token",
})
if err != nil {
	return err
}
// Interpret cookies for this site in caller-owned code. Do not log or persist
// cookie values.
```

This is an internal programmatic API, not an end-user browser setting or a
public Go package for external applications. It does not navigate the browser;
the selected origin must already have matching cookies in the browser profile.
Cookie values are returned to the Go caller, so callers must keep them in
memory and avoid logging or persisting them. `DockerBrowser.ReadBrowserSessionStorage`
supports exact-key, prefix, and all-key selection at one origin, with an
explicit `allowEmpty` choice. `DockerBrowser.RunBrowserAuthAdapter` is also
available to trusted in-repository callers that need the image to run a
site-specific adapter in-container and return only its bounded JSON result.
These operations have a 60-second maximum duration, including browser startup.

Site-specific session interpretation and OAuth handling belong to the package
that owns the site integration. The browser authentication helper image exposes bounded generic
session extraction and adapter execution without interpreting site credentials.

## Optional browser URL and tab limits

The daemon may pass `AACT_BROWSER_ACCESS_CONFIG_JSON` when creating
the container. The image validates it before starting Chromium and applies it
on each container start. An absent value leaves navigation and tab counts
unrestricted by this feature; the image's existing `file://*` block remains.
Invalid JSON or unsupported fields/patterns stop startup with a value-free
error.

Example:

```json
{
  "allowed_urls": [
    "https://app.example",
    "https://login.example"
  ],
  "blocked_urls": ["https://login.example/deny"],
  "auto_allow_popups": true,
  "popup_allowed_urls": ["https://app.example"],
  "tab_limits": {
    "max_tabs": 3,
    "hard_max_tabs": 8,
    "per_pattern": [
      {"pattern": "https://app.example", "max_tabs": 2}
    ],
    "popup_exemptions": [
      {"pattern": "https://login.example", "max_tabs": 3, "grace_seconds": 15}
    ]
  }
}
```

AACT's daemon validates these fields before starting the image. Blocked URLs
take precedence over allowed URLs when the daemon handles browser requests.

`allowed_urls`, `blocked_urls`, and `popup_allowed_urls` are optional arrays of at most 100
patterns, each at most 2048 characters. A non-empty `allowed_urls` enables
Chromium's managed URL allowlist: the image blocks `*`, allows the listed URL
patterns, and retains the existing `file://*` block. `blocked_urls` enables a
static block-all barrier for main-frame and subframe navigation, then the
extension installs validated per-session allow and deny rules. Deny rules have
higher priority, so they win when a URL matches both lists. The extension also
installs a session block-all fallback before adding exceptions. If validation
or exception installation fails, at least one barrier remains active and
HTTP(S) navigation stays blocked. If Chromium cannot install either barrier,
the extension shows a warning that navigation may be unrestricted. Redirects
and popup destinations follow the same navigation rules. The deny list controls browsing and navigation only; it does not block
page assets, fetch/XHR requests, or other non-navigation traffic. Empty or
absent arrays leave their respective URL policy unrestricted.
`popup_allowed_urls` names additional popup initiator URLs. When
`auto_allow_popups` is true, popup initiators matching `allowed_urls` are also
permitted, and `popup_allowed_urls` are added to those sources. `true` requires
a non-empty `allowed_urls`; false or absent leaves only explicit
`popup_allowed_urls` in effect. Popup destinations must still match
`allowed_urls` when that list is configured, and the tab limits still apply.
URL patterns use a
supported subset of Chrome URL blocklist filters: explicit `http://` or
`https://`, a host, optional port, and optional path prefix. A bare host such
as `example.com` matches that host and its subdomains; `.example.com` matches
only the exact host; `*` matches any host. IPv4 and bracketed IPv6 hosts are
supported without wildcards. Ports may be omitted (all ports), be a number
from 1 to 65535, or be `*` (all ports). Paths are case-sensitive prefixes.
Credentials, query strings, fragments, wildcard path fragments, other
schemes, and Chromium patterns outside this subset are rejected. The image
fails closed on invalid configuration. URL policy controls navigation; the
extension applies the same host, port, and path-prefix rules when counting
tabs. Path matching follows Chrome's character prefix: `/foo` also matches
`/foobar`; use `/foo/` when a slash boundary is intended. See the official [Chrome URL pattern format](https://chromeenterprise.google/policies/url-patterns/)
and [URL blocklist filter format](https://support.google.com/chrome/a/answer/9942583?hl=en)
for details.
Path patterns must use ASCII with non-ASCII characters percent-encoded, so
policy matching agrees with Chromium's serialized URL paths. Allow and deny
patterns are limited to a conservative compiled-regex size. The helper rejects
patterns outside that bound before writing policy. The extension checks each
generated regex with Chromium before installing session rules and shows a
warning badge if validation or installation fails. It identifies the
unrestricted case if Chromium cannot apply either block-all barrier.

`tab_limits` is optional. When present, `max_tabs` and `hard_max_tabs` are
required integers from 1 to 100, with `hard_max_tabs >= max_tabs`.
`per_pattern` entries are optional `{pattern, max_tabs}` objects; every
matching limit applies, so overlapping patterns all count. `popup_exemptions`
entries are optional `{pattern, max_tabs, grace_seconds}` objects, with
`grace_seconds` from 0 to 30. A matching popup-created tab (one with an opener)
can be excluded from the ordinary `max_tabs` count, up to the exemption's own
`max_tabs`; it still counts toward matching `per_pattern` limits and always
counts toward `hard_max_tabs`. A new `about:blank` popup may use the configured
grace period while its destination resolves. It counts toward the hard cap
during that period. Overlapping popup exemptions all apply; blank-popup grace
lasts for the longest configured grace period. The extension
counts tabs across browser windows and closes the newest eligible created tab
that exceeds a configured limit, including when a blank popup's grace expires;
it does not close an existing/initiating tab to repair a pre-existing excess.
Tabs may appear briefly before the event handler runs,
and simultaneous popup creation can race, so tab limits are best-effort
usability controls rather than a security boundary. URL allowlists are browser
navigation policy, not an outbound network firewall.

Authentication redirects vary by provider, tenant, and sign-in configuration.
Add the actual application and redirect patterns for the deployment; identity
provider domains are not built into the image.

After changing this image or its helper, rebuild it from the repository root:

```sh
docker build --pull --tag aact/browser-authentication-helper:1.18.0-form1 images/browser-authentication-helper
```

For a currently open page, the helper uses a fixed local Chrome DevTools
Protocol endpoint at `127.0.0.1:9222`. It matches the exact requested origin,
then reads sessionStorage with `getItem` and cookies with `Network.getCookies`;
it does not navigate, click, or change page state. The debug listener binds to
container loopback and port 9222 is not published to the host. If no matching
page is open or the listener is unavailable, the helper falls back to the
profile parsers. The offline sessionStorage parser reads LevelDB in place,
hashes relevant files before and after parsing, retries a bounded number of
times if they change, and fails closed if they remain active. An adapter may
request an empty match set for first-run sign-in, but defaults to an error for
missing keys.

Live cookie reads use the local CDP endpoint, which lets Chromium return
cookies without exporting its database. The offline fallback uses SQLite
read-only transactions. The image's Linux Chromium launch uses
`--password-store=basic`, so Chrome v10 cookies generated by this image can be
decrypted in memory. Cookies using another encryption scheme, including
system-keyring v11 entries, fail closed with an unsupported-format error. The
helper never exports cookie files or writes a decrypted cookie to disk. It
checks the database for concurrent changes and retries boundedly.

Site adapters must keep refresh credentials inside the browser container and
return only the output required by their caller. For example, a refresh-token
adapter should return an access token and its expiry, never the refresh token.
The generic helper cannot infer a site's intended output fields; the adapter
owns that contract. The daemon captures helper stdout in memory and
must not log it.

AACT hands the MCP its instance-scoped daemon credential through the standard
private tmpfs handoff. The daemon checks that credential and the bound container
before allowing browser operations. Adapter source is loaded from the capability
on the host and registered through the daemon's local control API; MCP requests
cannot supply executable adapter source. The browser profile remains a separate
Docker volume; it is not a Vault record.

### Calling the daemon from a capability

Declare `[mcp.browser_controller]` in the capability manifest with an optional
`profile_volume_input`, optional `access_config_input`, optional `startup_url`,
and optional `adapter_path` relative to the capability directory. The adapter
is trusted code from the installed capability; callers cannot submit source.
When no profile volume input is declared, AACT derives a separate volume name
for each Capability Instance. An optional `when` table enables the controller
only when the listed input values match.
AACT passes the daemon address, pinned TLS certificate, and the MCP's existing
credential in `/run/aact/vault.json`.

Send an authenticated `POST /v1/browser/credentials` to that daemon with a
selector, an optional exact `origin`, optional non-secret adapter `config`, and
an optional `private` object containing secrets for the trusted adapter. The
daemon passes the private object only in memory to the adapter as
`config["__private"]`; it must not be returned or logged.

```json
{"origin":"https://example.invalid","selector":{"kind":"cookie","match":"exact","names":["session"]},"config":{}}
```

`none` invokes the adapter without reading browser storage and starts the
browser directly. `cookie` selectors use `names`; `localStorage` and `sessionStorage` selectors
use `name` and `match` (`exact`, `prefix`, or `all`). The origin must be
permitted by the configured browser policy. The daemon first runs the helper
against a read-only profile without starting Chromium. If that does not yield
a usable result, it starts the browser, reloads the origin, and retries. A
successful response is `200` with `{"result":{...}}`, and a browser started
for that request is stopped. If interaction is required, the daemon responds
`409` with `{"result":{"status":"interaction_required"},"viewer_url":"http://localhost:PORT"}`
and leaves the viewer available. The capability should present that URL to the
user and retry only when a tool request needs the credential again. The daemon
removes the helper container when its MCP is removed, while retaining the profile
volume.

The viewer is bound to loopback and has no viewer password. Any local process
or user able to reach its port can control the authenticated browser while it
is running; the MCP's daemon credential does not protect the viewer. Chromium
still has outbound network access. By default, the browser
profile is ephemeral, and `--rm` removes the container and its profile when it
stops. To keep a profile across container stops and recreation, create a
dedicated named volume and mount it over the Kasm user's home directory:

```sh
docker volume create aact-browser-profile

# Kasm runs as uid/gid 1000. Make the new volume writable by that user.
docker run --rm --user 0 --entrypoint sh \
  --mount source=aact-browser-profile,target=/profile \
  aact/browser-authentication-helper:1.18.0-form1 \
  -lc 'chown 1000:1000 /profile'

docker run --detach \
  --name browser-authentication-helper-persistent \
  --shm-size=512m \
  --publish 127.0.0.1:6903:6901 \
  --mount source=aact-browser-profile,target=/home/kasm-user \
  aact/browser-authentication-helper:1.18.0-form1
```

Open <http://localhost:6903> for this persistent session. Change the host-side
port (`6903`) if it is already in use.

The whole home directory is the mount point because Kasm's startup script
copies its default files into the home directory. Mounting only
`/home/kasm-user/.config/chromium` prevents that initialization from completing.
The named volume keeps browser profile files when the container is stopped or
removed; use `docker start browser-authentication-helper-persistent` to resume the same
container, or run the command above again after removing it to create a new
container with the same profile. Do not remove the volume if you want to keep
the profile. Persisting profile files does not guarantee that Chromium restores
open tabs or that a website will accept saved cookies or tokens after restart;
you may need to sign in again.

### Upgrading an AACT-managed browser container

After an AACT upgrade that changes the managed browser helper, an older AACT
browser container may still use the configured profile volume. AACT refuses to
start another browser while any container mounts that volume. Identify the
container and confirm which volume it uses. If it is the previous AACT browser,
stop and remove that container, then let AACT recreate it. Keep the profile
volume; do not remove it. Do not run two browser containers against the same
profile. AACT reports the conflict but does not stop or remove containers or
volumes automatically. For any other container using the volume, resolve that
mount conflict manually before retrying.

The profile can contain cookies, tokens, browsing history, and downloaded
files. Treat the named volume as sensitive, keep it dedicated to this browser,
and do not share or expose it. Do not put authentication material in container
logs or diagnostic output. Any local user or process can control the browser
while it is running, so do not expose this port beyond the local machine or use
this image for shared access.

The image disables KasmVNC Basic Auth and permits plain HTTP for local access.
HTTPS remains available with the base image's self-signed certificate; use
`http://localhost:6901` to avoid certificate warnings.
