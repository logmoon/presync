# AGENTS.md — presync

This file is read first by any AI coding agent. It defines the skills available in this project and how to use them.

---

## Skills

| Skill | When | Purpose |
|---|---|---|
| `/architect` | Before building any feature | Think through decisions before touching code |
| `/remember save` | End of every session | Compress session state into memory.md |
| `/remember restore` | Start of every session | Restore full context before continuing |
| `/review` | After building any feature | Verify correctness, not just that it works |
| `/recover` | When something breaks | Diagnose failure mode before attempting fixes |
| `/imprint` | After the user confirms a UI feature is done | Capture visual patterns to ui-registry.md |

---

## Session Protocol

Every session has two phases.

### Phase 1 — Planning

1. Run `/remember restore` — reload memory.md and all context files before anything else
2. Read `context/progress-tracker.md` — know exactly where things stand
3. Run `/architect` — think through the feature before touching code. Present the plan and wait for explicit approval before proceeding

Do not write any code until the user has explicitly approved the plan.

### Phase 2 — Building

1. Build the feature
2. Run `/review` — report findings and stop. Do not proceed further
3. Wait for the user to test and confirm. The user may request fixes — make them and re-run `/review` as needed. Repeat until the user explicitly says they are satisfied
4. Only after the user explicitly confirms they are happy: run `/imprint` to capture UI patterns to ui-registry.md
5. Update `context/progress-tracker.md`
6. Run `/remember save`

### Definition of Done

A feature is only done when:
- `/review` has run, all issues are resolved, and the **user has explicitly confirmed they are satisfied**
- `context/progress-tracker.md` is updated
- `/remember save` has run

Do not run `/imprint` or `/remember save`, update the progress tracker, or consider a feature complete until the user says so. After `/review` passes, stop and wait — do not proceed autonomously.

---

## Context Files

| File | Purpose |
|---|---|
| `context/project-overview.md` | What this is, who it's for, core user flows |
| `context/architecture.md` | Stack, folder structure, system boundaries, invariants |
| `context/build-plan.md` | Phased feature list — numbered, ordered, with sub-tasks |
| `context/progress-tracker.md` | Live checklist — what's done, in progress, next |
| `context/code-standards.md` | Naming, structure, error handling, patterns |
| `context/library-docs.md` | Project-specific usage patterns for every third-party library. Context7 is configured — fetch live docs automatically before using any library. |
| `context/ui-tokens.md` | Design tokens — all colors, spacing, typography. Never hardcode values. |
| `context/ui-rules.md` | Layout, component patterns, do-nots |
| `context/ui-registry.md` | Living record of built components — read before building any new one |

---

## Library Docs — Context7

This project has Context7 configured as an MCP server. Whenever you are about to write code that uses a third-party library, call the Context7 MCP tool first to resolve the library ID and fetch current documentation. Do this automatically — do not wait to be asked. Relying on training-data knowledge for library APIs leads to outdated patterns and bugs.

Workflow:
1. Resolve the library: `mcp__context7__resolve-library-id({ libraryName: "..." })`
2. Fetch the relevant docs: `mcp__context7__get-library-docs({ context7CompatibleLibraryId: "...", topic: "..." })`
3. Then write the code

---

## Order of Authority

```
memory.md + progress-tracker → architecture.md invariants → code-standards.md → library-docs.md → general knowledge
```

Never rely on general training knowledge for library APIs — they change. Always check library-docs.md and use Context7 to fetch live docs before using any library.

---

## Invariants

_See `context/architecture.md` — Invariants section._
