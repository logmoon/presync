# UI Tokens — presync

Dark-by-default, neutral base, single warm accent used sparingly (primary actions, active/progress states only — never as a decorative or background color).

## Colors

### Accent (brand)
| Token | Hex | Use |
|---|---|---|
| `accent-base` | `#D97757` | Primary buttons, active nav item, progress bar fill |
| `accent-dark` | `#B85C3F` | Hover/pressed state for primary buttons |
| `accent-light` | `#E8A088` | Subtle highlights, focus ring on inputs |
| `accent-muted` | `#3A2A22` | Accent-tinted background (e.g. selected row), used very rarely |

### Backgrounds
| Token | Hex | Use |
|---|---|---|
| `bg-page` | `#1A1918` | App/page background |
| `bg-card` | `#232221` | Cards, panels, the Profiles list rows |
| `bg-secondary` | `#2A2928` | Nested surfaces inside a card (e.g. a history row inside the Profiles card) |

### Text
| Token | Hex | Use |
|---|---|---|
| `text-primary` | `#F2F0EE` | Headings, primary body text |
| `text-secondary` | `#B5B0AB` | Secondary copy, descriptions |
| `text-muted` | `#7A756F` | Timestamps, disabled labels, placeholder text |

### Status
| Token | Hex | Use |
|---|---|---|
| `status-success` | `#5FA876` | Sync complete, "done" file state |
| `status-warning` | `#D9A441` | Stale session, auto-sync cooldown notice |
| `status-error` | `#C9594B` | Transfer error, checksum failure after retries |
| `status-info` | `#5C92B8` | Neutral informational state (e.g. "idle") |

### Borders
| Token | Hex | Use |
|---|---|---|
| `border-default` | `#34322F` | Card borders, dividers |
| `border-focus` | `#D97757` (= `accent-base`) | Input focus ring |

## Typography

| Element | Size | Weight | Line Height | Color Token |
|---|---|---|---|---|
| Page title (H1) | 24px | 600 | 32px | `text-primary` |
| Section heading (H2) | 18px | 600 | 26px | `text-primary` |
| Card title | 16px | 600 | 24px | `text-primary` |
| Body | 14px | 400 | 20px | `text-primary` |
| Secondary body | 14px | 400 | 20px | `text-secondary` |
| Caption / timestamp | 12px | 400 | 16px | `text-muted` |
| Button label | 14px | 600 | 20px | inherits button text color |

Font family: system UI stack (`-apple-system, "Segoe UI", Roboto, sans-serif`) — no webfont loading, keeps WebView startup fast.

## Spacing

8px base scale: `4, 8, 16, 24, 32, 48` — used directly as pixel values in CSS (no Tailwind in this project; vanilla CSS custom properties).

```css
:root {
  --space-1: 4px;
  --space-2: 8px;
  --space-3: 16px;
  --space-4: 24px;
  --space-5: 32px;
  --space-6: 48px;
}
```

- Page padding: `--space-4` (24px) on mobile WebView width, `--space-5` (32px) on wider browser viewports
- Gap between cards/sections: `--space-3` (16px)
- Gap inside a card (between elements): `--space-2` (8px)

## Component Tokens

### Card
- Background: `bg-card`
- Border: `1px solid border-default`
- Radius: `12px`
- Padding: `--space-3` (16px)
- Shadow: none (flat dark UI, no elevation shadows — rely on background contrast instead)

### Button — Primary
- Background: `accent-base`, hover: `accent-dark`
- Text: `#1A1918` (dark text on the warm accent for contrast, not white)
- Radius: `8px`
- Padding: `8px 16px`
- Font: per Button label token above

### Button — Secondary
- Background: transparent
- Border: `1px solid border-default`
- Text: `text-primary`, hover background: `bg-secondary`
- Radius: `8px`
- Padding: `8px 16px`

### Input
- Background: `bg-secondary`
- Border: `1px solid border-default`, focus: `1px solid border-focus`
- Text: `text-primary`, placeholder: `text-muted`
- Radius: `6px`
- Padding: `8px 12px`
