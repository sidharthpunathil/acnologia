<!-- boardui:rules:start -->
# BoardUI design rules

This project uses BoardUI (React + Tailwind CSS v4, source-owned components under `components/`). These rules always apply when writing UI code. MCP tools are on demand; these rules are not optional context.

## Components first

- Before hand-building any UI element, check for an installed BoardUI component under `components/base/` and `components/application/`, and prefer it.
- Missing a component? Install it (BoardUI MCP `install_components`, or `npx boardui@latest add <name>`) instead of writing a lookalike.
- Import through the `@/` alias, e.g. `import { Button } from "@/components/base/buttons/button"`.

## Color: semantic tokens only

- Never use raw palette classes (`text-gray-500`, `bg-white`, `border-neutral-200`) or hex/oklch literals. Every color rides a BoardUI semantic token, which also makes dark mode automatic.
- Text: `text-text-primary`, `text-text-secondary`, `text-text-tertiary`, `text-text-placeholder`, errors `text-text-error-primary`.
- Surfaces: `bg-background-primary-default`, `bg-background-secondary-default` / `-hover`, `bg-background-tertiary-default`, page ground `bg-background-full`.
- Borders: `border-border-button-default` / `-hover`, hairlines `border-separator-border`, tables `border-border-table`, errors `border-border-error-default`.
- Icons: `text-foreground-icon-primary` through `text-foreground-icon-quaternary`.
- Charts: the `chart-1` … `chart-5` tokens (plus `-active` variants). CTAs and selection states: the `accent-50` … `accent-950` ramp.
- Dark mode flips tokens via the `.dark` class on `<html>`. Do not write `dark:` overrides with raw colors; if a token pair looks wrong in dark mode, pick a different token, not a literal.

## Typography: composite utilities only

- Use BoardUI's composite type utilities: `text-title-1-medium`, `text-title-2-medium`, `text-title-3-semibold`, `text-headline-medium`, `text-body-medium`, `text-body-regular`, `text-body-2-*`, `text-caption-1-semibold`, and friends. Each sets size, weight, line-height, and letter-spacing together.
- Never rebuild type by stacking `text-sm font-medium leading-5`; if a style seems missing, look in `styles/typography.css` before inventing one.

## Spacing and shape

- Stay on Tailwind's spacing scale (`gap-2`, `p-4`, `mt-6`); prefer flex/grid `gap` over per-child margins. Arbitrary values (`p-[13px]`) only when matching an existing BoardUI component exactly.
- Cards and panels: `rounded-3xl` with `border-border-button-default`. Inputs and menu rows: `rounded-md` to `rounded-xl`. Pills: `rounded-full`.

## Mechanics

- Direction: wrap the app (or a mixed-language region) in `DirectionProvider` from `@/components/foundations/direction/direction`, with an explicit BCP 47 `locale` (`ar`, `he`, `en-US`, `zh-CN`). Set matching `lang` and `dir` on `<html>` for full-app RTL. CSS `dir` alone does not configure React Aria keyboard navigation or portal placement. Use `useDirection()` for custom interactions and put its value on custom portal roots.
- Use logical styles: `ms`/`me`, `ps`/`pe`, `start`/`end`, `text-start`/`text-end`, `border-s`/`border-e`, `rounded-s`/`rounded-e`, and inline logical CSS properties. Mirror navigation/disclosure glyphs and horizontal drawer motion in RTL. Preserve physical coordinates for measured geometry, charts, canvas, and decorative art. Keep code, OTP digits, email addresses and URLs in explicit LTR regions; isolate mixed text with `<bdi>` or `dir="auto"`. Translating labels and formatting application data is separate from layout direction. See https://www.boardui.com/rtl.

- Internal vertical scroll regions must soften the top edge with a progressive blur and surface fade once scrolled, rather than sharply clipping content. Keep headers outside the scroll region. Use a stationary, `pointer-events-none`, `aria-hidden` overlay in a relative wrapper: a 24px masked 1px backdrop blur, a 16px masked 4px backdrop blur, and a gradient from the enclosing semantic surface token to transparent. Ramp each layer's opacity from 0 to 1 over the first 24px of scrolling; keep it invisible at scrollTop 0. Apply opacity to the individual layers, never the overlay parent (a translucent parent creates a backdrop root and can break blur at normal browser zoom). Verify top/reset, overflow, light/dark mode, and 100% zoom; preserve keyboard scrolling and do not blur the whole content area. The `scroll-fade` registry item ships this recipe as `ScrollFade` + `useScrollFade` (`@/components/foundations/scroll-fade/scroll-fade`); reuse it rather than rebuilding the layers.

- Merge classes with `cx()` from `@/utils/cx` (tailwind-merge aware of BoardUI's composite text styles). No string concatenation, no plain `clsx`.
- Icons come from `@remixicon/react`, passed as component references (`leadingIcon={RiAddLine}`), not rendered elements.
- Form components build on `react-aria-components`; extend the installed BoardUI form components rather than raw `<input>`/`<select>`.
- Focus states: `outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring`.

When unsure about a token, a component's API, or working example code, ask the BoardUI MCP server: `get_theme`, `get_component`, `get_usage_examples`.
<!-- boardui:rules:end -->

# Acnologia on BoardUI

Outside the BoardUI fence above, so `npx boardui init` leaves it alone.

- Paths: BoardUI lives under `web/src` (`src/components/{base,application,foundations}`, `src/styles`, `src/utils`). `@/` maps to `web/src`. Run `npx boardui@latest add <name> --yes` from `web/`; then pin any npm dependency it adds to an exact version at least two weeks old.
- Theme: `src/styles/acnologia.css` is the only place to retheme (violet accent ramp, warm neutrals, semantic overrides for both modes). Do not edit `src/styles/theme.css`.
- Dark mode: `stores/theme.ts` sets the `.dark` class and `data-theme` on `<html>` (key `acn.theme`, System, Light, Dark). The pre paint script in `index.html` mirrors it.
- Legacy class names are aliases of BoardUI tokens and keep working while pages migrate: canvas = `background-full`, surface = `background-primary-default`, soft = `background-secondary-default`, well = `background-tertiary-default`, ink = `text-primary`, ink-strong = `foreground-icon-hover`, muted = `text-secondary`, faint = `text-tertiary`, line = `border-button-default` (also `separator-border`, `border-table`), line-strong = `border-button-hover`, spark = `chart-2`, brand = `accent-600` light / `accent-300` dark. New code uses the BoardUI names.
- Status hues: red, emerald, amber, sky, violet, orange and stone flip in dark mode for old pages (`bg-red-50 text-red-700` reads right in both). New code uses `bg-status-*-background text-status-*-text` and `text-text-error-primary`.
- Stageshow (`src/stageshow`, `src/demos`) keeps its own `@layer stageshow`, declared below Tailwind's layers in `src/index.css`.
