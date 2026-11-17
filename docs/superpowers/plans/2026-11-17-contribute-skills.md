# Contribute Skills Implementation Plan

**Goal:** Ship a configurable skill that guides contributions to the pack's source repository.

**Architecture:** A bundled template consumes required Git host and repository name inputs. Agents resolve those via git-repo-map and follow the target repository's instructions.

**Tech Stack:** AACT package TOML, Mustache, Markdown.

**Spec:** docs/superpowers/specs/2026-11-17-contribute-skills-design.md

## Constraints and review focus

No runtime changes, machine-specific settings, or edits to the consuming pack. Preserve work and authorization rules. Check configured identity, missing map entries, multiple checkouts, local instructions, and PR authorization.

### Task 1: Add and verify the capability

- Create `packages/contribute-skills/package.toml` and `packages/contribute-skills/SKILL.md.mustache`.
- Update `README.md` and `docs/capability-packs.md` with configuration and usage.
- [ ] Demonstrate the missing capability in an isolated CLI catalog.
- [ ] Add the package and documentation using existing template and input conventions.
- [ ] Install using isolated state and agent home with two different repository identities; inspect the generated skill and check required input validation.
- [ ] Run relevant package tests and review the resulting diff.
