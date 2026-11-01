---
name: review
description: After building a feature, verify it matches what was planned, respects the system architecture and design standards, and is ready for production. Dispatches a fresh reviewer that freezes a candidate ledger, investigates by risk, converges, and reports confirmed findings only.
---

## Hard rule

Reviewing is reporting. Never fix what you find. Report it and stop — the primary session waits to be asked; the reviewer never waits.

---

Building is not done when the code runs. It is done when the code is correct.

AI moves fast. Fast means things get built that work on the surface but drift from the architecture, violate the design system, or miss edge cases that matter. This skill catches those things before they compound into bigger problems.

Run this after every feature. Before you move on.

## Run With Fresh Eyes

Do not review in the same session that built the feature — the session that just wrote the code is biased toward believing it is correct, and its context is already full of its own reasoning. Dispatch this project's `reviewer` subagent (defined in `opencode.json`; read-only, bash limited to `git diff`). Invoke with `@reviewer`, or the `task` tool when you need the task ID for a targeted second pass.

The reviewer already has every context file in its instructions. Hand it a compact brief, not a copy of the codebase:

| Field | What to include |
|---|---|
| Goal and decisions | What was planned, the key decisions, acceptance criteria. Not the full plan transcript. |
| Paths | Exact changed paths and untracked files, from `git status --porcelain` / `git diff --stat` that you run yourself. |
| Skip | Unrelated or generated files the reviewer should not spend time on. |
| Focus | 2–5 review questions or risk flags from the plan — auth, payments, migrations, permissions, data loss, concurrency, or non-obvious control flow. If risk is unclear, treat it as high. |
| Diff command | One command, e.g. `git diff -U10 HEAD -- <paths>`, or the branch range. |
| Tests | The test command and result if `/test` ran. |

Do not paste context files, full source files, or the whole diff. The reviewer reads what it needs.

The reviewer runs a structured pass:

1. **Plan** — reads the diff once, writes a coverage ledger (file/hunk, risk, state) and a frozen set of specific candidates. New candidates found later go to `DEFERRED`.
2. **Investigate** — resolves each candidate to `CONFIRMED`, `REJECTED`, `UNVERIFIED`, or `DEFERRED`. HIGH-risk candidates may read cross-file context; NORMAL gets one targeted read; LOW is diff-only.
3. **Converge and summarize** — stops when no candidate is open and no new state is produced, then reports. The guard reserves the last two steps for the report.

The reviewer reports and returns; it never asks a question or waits for the developer.

## Scope

Review the diff, not the repository. The reviewer does not run `git status`, `git log`, or `git show`, does not tour files, and does not re-read a region. The diff contains both sides of the change; context files are for checking rules, not for reading end to end.

Stop when the candidate ledger is terminal. A `DEFERRED` or `UNVERIFIED` candidate is a finished first pass, not a failure — the developer decides whether it needs a second look.

## Depth

Depth is earned by risk, not by diff size. A large generated file can be low risk; a one-line change to billing or auth can be high risk. Verify uncertain claims against the changed code, but do not chase every uncertainty: a candidate that is still open after one targeted look becomes `UNVERIFIED`.

Do not re-run layers or manufacture issues to look thorough — a clean diff gets a clean report.

## What This Skill Does Not Do

It does not fix anything. It reports what it finds and lets the developer decide what matters and what to do about it.

---

## Step 1 — Brief

Establish the benchmark from the brief:

- The plan's goal, decisions, and acceptance criteria
- The exact changed paths and untracked files
- The relevant rules in the context files — architecture boundaries, code standards, design rules

If the brief is missing the goal, the primary asks the developer before dispatching. The reviewer reports missing context as a blocker instead of waiting.

---

## Step 2 — Review the Diff

The reviewer applies three lenses **once per candidate**, not as three separate passes:

- **Plan alignment** — does the change implement the brief, and nothing outside it?
- **System integrity** — does it respect architecture boundaries, the design system, code standards, and existing patterns?
- **Production readiness** — error handling, edge cases, obvious bugs, and the test gap.

A candidate is a concrete way the change could be wrong, not a category to browse. "Check error handling" is not a candidate; "the push handler writes the file before verifying the hash" is.

---

## Step 3 — Relay the Report

For a clean diff, one line is enough:

```
No issues found — [feature] is ready to ship.
```

For findings:

```
## Review — [Feature Name]

VERDICT: SHIP | SHIP WITH FIXES | BLOCK | INCOMPLETE
COVERAGE: [files/hunks covered; confirmed / rejected / unverified / deferred]
FINDINGS:
- file:line — CRITICAL|IMPORTANT|MINOR — problem — evidence
UNVERIFIED / DEFERRED:
- candidate — what was not resolved, and why
```

Only `CONFIRMED` findings belong in `FINDINGS`. Hedges, speculation, and single-read guesses belong in `UNVERIFIED` or `DEFERRED`. Do not expand empty sections to look thorough.

---

## Step 4 — Let the Developer Decide

Present the report and stop. Do not start fixing. Do not suggest fixes unless the developer asks.

Wait for the developer to:

- Ask you to fix a specific issue
- Tell you an issue is intentional and can be ignored
- Confirm the remaining findings are acceptable and the feature is ready to move on

### Fixes and re-review

Judge confidence before re-reviewing. A small, local fix verified by tests or a build/typecheck, touching no architecture, data flow, or invariant, does not need a second review — report what changed and what verified it.

Otherwise send only the new diff for one re-review. Run a full feature review again only if the fix changes architecture, data flow, or another invariant.

If the first pass returned `INCOMPLETE`, or the developer wants the `UNVERIFIED`/`DEFERRED` candidates resolved, resume the same reviewer session with only those candidates. That is a targeted second pass and counts as the second review round. Do not re-dispatch a full review just to complete the ledger.

The loop is capped at two review rounds per feature. If the second round still finds issues, report them and stop. No third round unless the developer explicitly asks for one. Never re-dispatch the reviewer just to confirm a clean result.

The developer owns the quality decision. You inform it.

---

## Severity Guide

**Critical — fix before moving on**

- Architecture boundary violations that will break future features
- Missing error handling that causes silent failures
- Functionality that was planned but completely missing

**Important — fix soon**

- Design system drift that will cause UI inconsistency
- Code standard violations that will compound across the codebase
- Edge cases that a real user will encounter

**Minor — fix when convenient**

- Naming inconsistencies that do not affect behaviour
- Missing optimisations
- Style issues that do not affect the design system

Label each finding with its severity so the developer can triage quickly.

---

## The Standard

The question this skill answers is not "does it work?"

The question is "is it correct?"

Working and correct are not the same thing. A feature can work today and break the project tomorrow. Review exists to catch the difference.
