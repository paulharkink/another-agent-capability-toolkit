# AACT overnight handoff

## Try the app

The native macOS arm64 development release is unpacked at:

```
/Users/user/sources/another-agent-capability-toolkit/dist/native/bin/aact
```

Run it inside the prepared consumer checkout to browse all 15 selected public
and repository-specific skills/packages:

```sh
cd /tmp/codex/aact-consumer/agent-skills
/Users/user/sources/another-agent-capability-toolkit/dist/native/bin/aact
```

Add `--state-dir /tmp/aact-review-state` for an isolated review. Opening the
manager does not install packages; install actions target the selected agent
homes. The consumer worktree is preserved for review. The original checkout and
legacy launcher remain available.

## Delivered

- A native Go CLI and persistent Catalog/MCPs/Agents/Settings TUI.
- Mixed public/private checkout catalogs and existing environment TOMLs.
- Typed prefilled forms, collections, native/terminal path pickers, and
  noninteractive CLI input.
- Direct Mustache rendering and optional language-independent JSON generators.
- Global selected-agent skill installation, ownership checks, safe uninstall,
  MCP adapters and generic manual config artifacts.
- Docker MCP lifecycle, global inventory, health, logs, preparation and explicit
  authentication.
- Seven public packages, including the portable Git repository mapper.
- Legacy migration preview/apply logic verified only against isolated fixtures.
- Six platform/architecture archives, checksums, versioned bootstrap installers
  and native/container/archive CI.

## Review

- [Public implementation draft PR](https://github.com/paulharkink/another-agent-capability-toolkit/pull/1).
- [Consumer integration draft PR](https://github.com/paulharkink/agent-skills/pull/26).
- [Verification evidence](implementation-evidence.md).

No main merge, release publication, real agent configuration change, live
credential migration or vendor login was performed.

## Remaining checks

Native GUI picker visual checks and real-account inspector authentication are
manual follow-up. Marketplace readers/installers remain deferred as agreed.
Native Linux, macOS and Windows test/vet CI passed after repairing the initial
Windows failures. Linux/macOS race checks and real Docker CI also passed.
See the verification evidence for exact runs and regression details.
