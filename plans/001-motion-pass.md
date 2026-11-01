# 001: Motion pass for the dashboard

Commit: 5433184. Source: improve-animations audit (Emil Kowalski's rules).

## Findings (vetted)
| # | Sev | Category | Location | Finding | Fix |
|---|---|---|---|---|---|
| 1 | HIGH | Accessibility | web/src/index.css reduced-motion block | Forces every animation to 0.01ms x1, which freezes loading spinners and removes helpful fades. | Reduced motion keeps opacity fades and spinners, drops movement and press scaling. |
| 2 | MEDIUM | Cohesion | index.css, Button | No easing tokens; built-in ease-out and one hand-typed curve. | Add --ease-out cubic-bezier(0.23, 1, 0.32, 1) and --ease-in-out cubic-bezier(0.77, 0, 0.175, 1); use them for entrances and press. |
| 3 | MEDIUM | Interruptibility | components/Toaster.tsx, stores/toast.ts | Keyframe entrance, instant removal, stack jumps. | Transition entry with @starting-style, 140ms exit (opacity + translateY(4px) + scale(0.98)) before removal. |
| 4 | MEDIUM | Missed | components/Modal.tsx | Animates in, snaps out. | Keep mounted 150ms on close with a reverse animation; cache last content so it does not blank while leaving. |
| 5 | LOW | Easing | .skeleton | Constant shimmer uses ease-in-out. | linear. |
| 6 | LOW | Feedback | Catalog and Providers cards | No press feedback; hover lift fires on touch. | active:scale-[0.99]; enable Tailwind hoverOnlyWhenSupported. |

## Values
- Entrances: 180 to 220ms, var(--ease-out). Exits: 140 to 150ms, ease-out, about 70% of the entrance.
- Press: scale(0.98) buttons, 0.99 cards, 140ms var(--ease-out).
- Modal is centered, so transform-origin center is correct (exempt).

## Verify
- DevTools Animations panel at 10 percent speed: modal and toast enter and leave without jumps.
- Rendering tab, emulate prefers-reduced-motion: spinners still spin, dialogs fade without moving.
- Touch emulation: tapping a card does not leave it lifted.

Status: DONE (implemented in the same session).
