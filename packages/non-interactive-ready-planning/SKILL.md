---
name: non-interactive-ready-planning
description: Use when the user asks for a plan, ticket, task, spec, or implementation brief to be ready for non-interactive execution, automation, AFK work, yolo mode, or another agent. The skill ensures all decisions are surfaced and resolved before execution, without inventing user decisions.
version: 1.0.0
---

# Non-Interactive Ready Planning

"Non-interactive ready" means: get every decision out of the way before execution starts.

A non-interactive implementer should be able to execute the plan without asking the user anything, waiting for permission, guessing intent, discovering an unresolved tradeoff, or needing a manual action that was not already planned.

It does **not** mean the agent may make user decisions. It means the planning agent must find the decision points early, ask the user, and iterate until the plan is clear enough to execute without interaction.

## Core Rule

Think all the way through the entire execution before finalizing the plan.

Find every point where execution might later need the user:

- a decision
- a permission
- a manual action
- a credential or secret
- an external service action
- a migration or cutover choice
- a risk acceptance
- a rollback choice
- a test target
- a definition of done

Resolve those points during planning.

## Research First, But Do Not Stall

Before asking, do enough read-only research to discover what questions are real.

Use low-impact, reasonably quick research:

- read repo files and local instructions
- search source code and configuration
- inspect schemas, manifests, CLIs, help output, and docs
- fetch relevant web pages or official docs
- inspect container images or packages locally when useful
- run dry-run or read-only commands
- inspect live systems only with read-only operations

Do not do implementation work during planning.

Do not make the user wait through a long investigation before the first useful question. If research is taking more than a few minutes, ask the first high-value questions with what is already known, then continue iterating.

## Ask Interactively

Use Plan Mode for this work. The planning conversation is the place where unresolved decisions are discovered, explained, and resolved before execution.

When the tool is available, ask decision questions with `request_user_input` so the session clearly enters a "waiting for response" interaction. Do not replace that with a plain text question unless the needed question cannot reasonably be expressed as a small set of choices.

Ask only 1-3 questions at a time.

Each question must remove a real future blocker or decision point. Do not ask questions that research can answer.

Before calling `request_user_input`, briefly explain what decision point was discovered, why it matters, and what tradeoff the choices represent. The user should understand the question before seeing the choice UI.

For each choice, make the impact concrete. Avoid vague labels like "Default" or "Other" as real choices. The recommended option should be first only when there is an evidence-based recommendation; otherwise present neutral choices and explain that no recommendation is available yet.

Good questions settle things like:

- which outcome is desired
- which option should win between real tradeoffs
- what risk is acceptable
- who performs a required manual step
- when a cutover should happen
- what exact data, names, targets, or policy should be used

After each answer, update the mental model, do any newly relevant read-only research, and ask the next set of questions.

Repeat until the plan is decision-complete.

## Do Not Invent Decisions

Never fill missing user intent with "reasonable defaults" just to make the plan look complete.

If a decision affects product behavior, policy, security, access, operations, data ownership, source of truth, cost, downtime, migration order, user-visible behavior, or manual action, ask the user.

If the user refuses to decide, the plan must either:

- mark the item as a blocker, or
- explicitly state that execution cannot be non-interactive.

## Plan Requirements

A non-interactive-ready plan must include:

- the exact goal
- success criteria
- what is in scope and out of scope
- implementation approach
- required user decisions already made
- required permissions already granted
- required manual actions and who performs them
- secrets and credential handling
- external systems involved
- migration or cutover sequence
- validation steps
- expected evidence of success
- rollback or recovery path
- known blockers or preconditions

The plan should be clear enough that an implementer can follow it mechanically.

## Final Check

Before presenting the final plan, ask:

- Would execution ever need to stop and ask the user something?
- Would execution require a permission not already granted?
- Would execution need a manual action not already scheduled or assigned?
- Would two reasonable implementers make different choices from this plan?
- Are there any hidden "configure later", "as needed", or "decide during rollout" gaps?
- Did I invent any decision that belongs to the user?

If any answer reveals a gap, continue planning instead of finalizing.
