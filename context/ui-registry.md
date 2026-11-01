# UI Registry

_Written by `/imprint` after building any UI component. Read this before building a new one — never duplicate a pattern that already exists here._

---

## Components

### Card

File: `web/style.css`
Last updated: 2026-06-21

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Background       | `var(--bg-card)` (#232221)     |
| Border           | `1px solid var(--border-default)` |
| Border radius    | `var(--radius-card)` (12px)    |
| Padding          | `var(--space-3)` (16px)        |
| Shadow           | none                          |

**Pattern notes:**
Cards are the primary container for all content sections. Every profile row, history entry, and settings group uses this shape. Cards never nest more than one level deep — a card may contain a `bg-secondary` row, but that row never contains another bordered card.

### Card — Title / Subtitle

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Title font       | 16px / 600 / `var(--text-primary)` |
| Subtitle font    | 14px / 400 / `var(--text-secondary)` |
| Spacing          | subtitle sits directly below title, no explicit gap |

### Button — Primary

File: `web/style.css`
Last updated: 2026-06-21

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Background       | `var(--accent-base)` (#D97757) |
| Hover            | `var(--accent-dark)` (#B85C3F) |
| Text color       | `var(--bg-page)` (#1A1918) — dark text on warm accent |
| Border radius    | `var(--radius-btn)` (8px)      |
| Padding          | `var(--space-2) var(--space-3)` (8px 16px) |
| Font             | 14px / 600                     |
| Disabled         | opacity: 0.4, cursor: not-allowed |

**Pattern notes:**
One primary button per screen/section max. Never more than one visible at once in the same view.

### Button — Secondary

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Background       | transparent                    |
| Border           | `1px solid var(--border-default)` |
| Text color       | `var(--text-primary)`          |
| Hover background | `var(--bg-secondary)`          |
| Border radius    | `var(--radius-btn)` (8px)      |
| Padding          | `var(--space-2) var(--space-3)` (8px 16px) |

**Pattern notes:**
Destructive actions (unpair, forget server) use Secondary style, not a separate red/danger button. Confirmed via text, not color.

### Button — Small variant

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Font             | 12px / 600                     |
| Padding          | `var(--space-1) var(--space-2)` (4px 8px) |

### Nav Bar

File: `web/style.css`, `web/index.html`
Last updated: 2026-06-21

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Layout           | flex, gap: `var(--space-4)`    |
| Border bottom    | `1px solid var(--border-default)` |
| Margin bottom    | `var(--space-4)` (16px)        |
| Item font        | 14px / 600                     |
| Item color (inactive) | `var(--text-secondary)`   |
| Item color (active)   | `var(--text-primary)` + `border-bottom: 2px solid var(--accent-base)` |
| Item hover       | color: `var(--text-primary)`   |
| Progress indicator | pulsing 6px dot (`var(--accent-base)`) appended to active Progress tab |

**Pattern notes:**
Four tabs max: Home, Progress (hidden when idle, shown with pulse during sync), Profiles, Settings. No icons-only nav — always pair a short label.

### Progress Bar

File: `web/style.css`
Last updated: 2026-06-21

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Track background | `var(--bg-secondary)` (#2A2928) |
| Track height     | 8px                            |
| Track radius     | 4px                            |
| Fill background  | `var(--accent-base)` (#D97757) |
| Fill transition  | width 0.3s ease               |

### Status Dot

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Size             | 8px circle                     |
| Idle             | `var(--status-info)` (#5C92B8) |
| Syncing          | `var(--accent-base)` + pulse animation |
| Success          | `var(--status-success)` (#5FA876) |
| Error            | `var(--status-error)` (#C9594B) |

### Mode Badge

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Background       | `var(--bg-secondary)`          |
| Text color       | `var(--text-secondary)`        |
| Font             | 12px / 600                     |
| Padding          | `var(--space-1) var(--space-2)` (4px 8px) |
| Border radius    | `var(--radius-input)` (6px)    |

**Pattern notes:**
Also used as the direction badge on push profile cards (label `push`), alongside the mode badge. Pull remains the unlabelled default.

### Empty State

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Text alignment   | center                         |
| Padding          | `var(--space-6) var(--space-3)` (48px 16px) |
| Primary text     | `var(--text-secondary)`        |
| Secondary text   | `var(--text-muted)`, 12px, `margin-top: var(--space-1)` |

**Pattern notes:**
Every section that can be empty must show one — never a blank card. Secondary line explains why it's empty and what to do. No buttons unless the action genuinely lives in the UI.

### File List Item

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Background       | `var(--bg-secondary)`          |
| Border radius    | `var(--radius-input)` (6px)    |
| Padding          | `var(--space-1) var(--space-2)` (4px 8px) |
| Font             | 12px / 400                     |
| Name color       | `var(--text-primary)`, overflow: ellipsis |
| State color      | `var(--text-muted)` (default), `.done` = `var(--status-success)`, `.error` = `var(--status-error)`, `.in_progress` = `var(--accent-base)` |

### Settings Row

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Layout           | flex, justify: space-between, align: center |
| Padding          | `var(--space-2) 0` (8px 0)     |
| Border between   | `1px solid var(--border-default)` on `.setting-row + .setting-row` |
| Label font       | 14px / `var(--text-primary)`   |
| Value font       | 14px / `var(--text-secondary)` |

### Toggle Switch

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Size             | 40px × 22px                    |
| Background (off) | `var(--bg-secondary)`          |
| Background (on)  | `var(--accent-base)`           |
| Border           | `1px solid var(--border-default)` (off), same as bg (on) |
| Border radius    | 11px (pill shape)              |
| Knob             | 16px circle, `var(--text-primary)`, 2px inset |
| Knob transition  | transform 0.2s, translateX(18px) when on |
| Disabled         | opacity: 0.4, cursor: not-allowed, `aria-disabled="true"` |

**Pattern notes:**
The Settings auto-sync toggle is disabled (with an explanatory `card-subtitle` caption below it) whenever the server has no eligible profiles, the client is not Android, the app is too old, or no sync folder is picked. Never leave a toggle clickable when the action cannot succeed.

### Loading State

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Text color       | `var(--text-muted)`            |
| Font             | 14px                           |
| Text alignment   | center                         |
| Padding          | `var(--space-6) 0` (48px 0)    |

### Input

File: `web/style.css`
Last updated: 2026-06-21

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Background       | `var(--bg-secondary)` (#2A2928) |
| Border           | `1px solid var(--border-default)` |
| Border radius    | `var(--radius-input)` (6px)    |
| Text color       | `var(--text-primary)`          |
| Font             | 14px / 400                     |
| Padding          | `var(--space-2) var(--space-3)` (8px 16px) |
| Placeholder      | `var(--text-muted)`            |
| Focus            | border-color: `var(--border-focus)` (#D97757) |
| Transition       | border-color 0.15s             |

**Pattern notes:**
Used for text inputs (pairing URL entry, device name). Always full-width inside its container. No label built into the component — pair with a card-subtitle or label element above it.

### Destination Chip

File: `web/style.css`
Last updated: 2026-06-21

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Font             | 12px / 600                     |
| Padding          | `var(--space-1) var(--space-2)` (4px 8px) |
| Border radius    | `var(--radius-input)` (6px)    |
| Border           | `1px solid var(--border-default)` |
| Background (unselected) | `var(--bg-secondary)`    |
| Text (unselected) | `var(--text-secondary)`       |
| Background (selected) | `var(--accent-base)`      |
| Text (selected)  | `var(--bg-page)` — dark text on warm accent |
| Border (selected) | `var(--accent-base)`          |
| Hover            | border-color: `var(--accent-light)` |
| Locked state     | opacity: 0.7, cursor: default |
| Transition       | background, color, border-color 0.15s |
| Layout           | flex container with `gap: var(--space-1)`, `flex-wrap: wrap` |

**Pattern notes:**
Used for multi-select destination filtering on profile cards. Click to toggle selection. When a profile has only one destination, the chip gets the `.locked` class and cannot be deselected. Selected chips use the accent color (same as primary buttons) to indicate active state. Chips sit below the card subtitle and above the mode badge / sync button row.

### Progress Card List

File: `web/app.js` (renderer), `web/style.css` (card)
Last updated: 2026-11-01

| Property         | Value                          |
| ---------------- | ------------------------------ |
| Container        | `#content.content` — flex column, `gap: var(--space-3)` (16px) |
| Card             | Reuses the Card pattern verbatim (`bg-card`, `border-default`, `12px`) |
| Title            | 16px / 600 / `var(--text-primary)` — profile name, or "Sync complete" |
| Subtitle         | 14px / 400 / `var(--text-secondary)` — "X of Y files", plus inline `status-error` error count and `status-warning` "paused" marker |
| Progress bar     | Existing Progress Bar pattern; fill width = done/total |
| Current file row | File List Item pattern, state label `sending` / `paused` / `in progress` |
| Action — live    | Button — Secondary, label `Stop` |
| Action — paused  | Button — Primary, label `Resume sync` / `Resume upload` |
| Action row       | `margin-top: var(--space-3)` (16px) |

**Pattern notes:**
Progress renders every session as its own card — active local transfer first, then the locally paused session, then any other non-complete session the server still tracks (newest first). Cards are deduped by profile+direction so the live/paused card never appears twice. Only the live card carries Stop; resumable cards carry Resume, and those buttons render disabled while another local sync runs (one sync at a time). No new colors, spacing, or component shapes — every card is the standard Card pattern.
