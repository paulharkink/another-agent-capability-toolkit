# Grafana Browser Login Implementation Plan

**Goal:** Allow Grafana Inspector to recover an expired session using credentials stored in AACT and a pack-configured browser login.

**Architecture:** Reuse the managed browser and its private daemon connection. Run bounded browser automation against explicitly configured login origins, then use the existing cookie and rotation machinery. Direct cookies and API tokens remain separate authentication modes.

**Tech Stack:** Go, Python, Chromium, Playwright, AACT vault and Docker.

## Global constraints

- Secrets are entered through AACT, remain scoped to the instance and do not appear in logs, process arguments or agent responses.
- Submit credentials only to the configured HTTPS login origin; use the configured CA and never disable TLS validation.
- Attempt automatic login only in browser-credentials mode when a session is missing or rotation fails; return a viewer URL when interaction is required.
- Exercise existing cookie/token modes and prevent concurrent requests from triggering a login stampede.
- Live OT tests use isolated Mac state and Docker resources, leaving existing configurations untouched.

## Tasks

- [x] Verify synthetic form inputs are saved as vault secrets and make the browser-credentials mode valid throughout Grafana preparation.
- [x] Add a bounded Playwright browser login operation with fixture tests for successful login, wrong origin and unexpected pages.
- [x] Connect the instance-scoped daemon and managed browser, including trusted CA and private credential transport.
- [x] Add Grafana automatic login and retry after rotation failure, with public-boundary recovery tests.
- [x] Configure the standalone OT test from the DSO preset, verify login and session recovery, then run required project checks.
