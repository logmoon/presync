# UI Rules — presync

## Layout

- Max content width: `640px`, centered — this is a status/control surface, not a data-dense dashboard, and the WebView is often phone-width anyway.
- Page padding: `--space-4` (24px) on narrow/WebView widths, `--space-5` (32px) on wider browser viewports.
- Gap between major sections on a page: `--space-3` (16px).
- No sidebar layout — this is a small, mostly single-column tool. Top nav only.

## Navigation

- Simple top tab bar with four items: **Home**, **Progress** (only visible/enabled during an active sync), **Profiles**, **Settings**.
- Active tab: `text-primary` + `accent-base` underline (2px). Inactive tabs: `text-secondary`, no underline.
- No icons-only nav — always pair a short label, since this is a small enough set of items that icon-only adds ambiguity for no space savings.
- Progress tab badges with a small pulsing accent dot while a sync is active; disappears entirely when idle (don't show a disabled/greyed tab — just omit it).

## Cards

Every content section (a profile row, a history entry, a settings group) uses the same card pattern from `ui-tokens.md`:
- `bg-card` background, `border-default` 1px border, `12px` radius, `16px` padding.
- Card title (16px/600) at the top, optional secondary line (14px/`text-secondary`) directly below it, content/actions below that.
- Cards never nest more than one level deep (a card may contain a `bg-secondary` row, but that row never contains another bordered card).

## Typography Hierarchy

- **H1 (24px/600)** — page title only, one per page, top of the page above any cards.
- **H2 (18px/600)** — section headers within a page when more than one logical group exists (e.g. "Profiles" then "Recently Synced").
- **Card title (16px/600)** — the name of the thing the card represents (profile name, device name).
- **Body (14px/400)** — all standard content text.
- **Caption (12px/400, `text-muted`)** — timestamps, byte counts, file counts. Always paired with content, never standalone as the only text in a region.

## Buttons

- **Primary** — one per screen/section, the single most important action: "Sync Now", "Confirm Pairing", "Sync All". Never more than one primary button visible at once in the same view.
- **Secondary** — everything else actionable but not the main action: "Cancel", "Forget this server", "View History".
- Destructive actions (unpair, forget server) use the **Secondary** style, not a separate red/danger button style — confirm via a simple text confirmation step instead of color-coding danger, to keep the palette restrained.

## Forms

This app has very few form inputs (mostly a server address field and toggles), but where they appear:
- Label above the input, 14px/600, `text-primary`, `--space-1` (4px) gap to the input.
- Input styling per `ui-tokens.md` Input component token.
- Error state: border becomes `status-error`, with a 12px caption below the input in `status-error` explaining what's wrong — never just a red border with no text.
- Submit/primary action button sits directly below the form, full-width on narrow viewports.

## Empty States

Every section that can be empty must show one — never a blank card or an empty list with no explanation.

- **No profiles configured:** card with a short line ("No sync profiles yet") + secondary text pointing at editing `config.toml` (profiles are server-config-driven, not created in the UI).
- **No sync history:** "Nothing synced yet" + the profile's last-synced state as "—".
- **No paired devices (server-side, if ever surfaced):** "No devices paired" + a hint to run `presync pair`.

Standard pattern: centered text block inside the existing card shape (not a special illustration or icon) — `text-secondary` primary line, `text-muted` secondary line, no buttons unless the action genuinely lives in the UI (most "empty" causes here are CLI/config-driven, so most empty states are informational only).

## Do Nots

- **Do not** use any color outside the defined token set — no ad-hoc hex values in component code.
- **Do not** use the accent color (`accent-base`) for anything except the single primary action and active/progress indication on a given screen — it must never become a background fill, a card border, or general decoration.
- **Do not** add loading spinners for state that's already known synchronously (e.g. reading already-fetched profile data) — per the design principle, nothing that should be instant gets a spinner.
- **Do not** use a destructive red/danger button style — destructive actions stay visually equivalent to Secondary buttons, confirmed via text, not color.
- **Do not** introduce a sidebar, hamburger menu, or any navigation pattern beyond the four-item top tab bar — this tool's surface area doesn't justify it.
- **Do not** show a blank card or list with no content and no explanatory text — every emptiable section has a defined empty state (see above).
- **Do not** use drop shadows or elevation effects — depth is communicated by `bg-card` vs `bg-page` contrast only.
- **Do not** load any external font, icon font, or JS framework — vanilla JS/CSS and system fonts only, per the WebView-performance requirement in the design doc.
