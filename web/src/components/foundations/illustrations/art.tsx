/*
 * Spot illustrations for empty states, the 404 page and the gallery at
 * /dev/illustrations. Original line art on a 120 by 96 board: one muted ink
 * line, one softer line, and a single violet accent per piece. Static on
 * purpose, so screenshots stay stable and nothing moves under reduced motion.
 */
import { Frame, dashed, paint, type IllustrationProps } from "./frame";

/** No keys yet: a flat split ring with one key hanging from it and the dashed slot of a missing one. */
export function KeyRingArt({ className }: IllustrationProps) {
  return (
    <Frame className={className}>
      <ellipse cx="60" cy="24" rx="17" ry="10" style={paint("ink")} />
      <path d="M44.5 27.5 A17 10 0 0 0 66 33.6" style={paint("soft")} />
      <path d="M50 32.3 V37" style={paint("ink")} />
      <circle cx="50" cy="44" r="7" style={paint("accent", "tint")} />
      <circle cx="50" cy="44" r="2" style={paint("accent")} />
      <path d="M50 51 V76 M50 68 H55 M50 73 H54" style={paint("accent")} />
      <path d="M71 31.7 V37" style={paint("soft", "none", dashed)} />
      <circle cx="71" cy="44" r="7" style={paint("soft", "none", dashed)} />
      <path d="M71 51 V76 M71 68 H76 M71 73 H75" style={paint("soft", "none", dashed)} />
    </Frame>
  );
}

/** No services: a shelf of empty tile slots, one waiting tile leaning in. */
export function ShelfArt({ className }: IllustrationProps) {
  return (
    <Frame className={className}>
      <rect x="22" y="37" width="22" height="22" rx="5" style={paint("soft", "none", dashed)} />
      <rect x="49" y="37" width="22" height="22" rx="5" style={paint("soft", "none", dashed)} />
      <path d="M60 44 V52 M56 48 H64" style={paint("accent")} />
      <rect x="77" y="36" width="21" height="21" rx="5" transform="rotate(9 87 47)" style={paint("accent", "tint")} />
      <rect x="16" y="61" width="88" height="5" rx="2" style={paint("ink", "paper")} />
      <path d="M27 66 V74 L33 66 M93 66 V74 L87 66" style={paint("ink")} />
      <path d="M30 24 H46 M30 29 H40" style={paint("soft")} />
    </Frame>
  );
}

/** No apps: a token card with a masked secret, a second card ghosted behind it. */
export function TokenCardArt({ className }: IllustrationProps) {
  const dots = [32, 36.5, 41, 45.5, 54, 58.5, 63, 67.5];
  return (
    <Frame className={className}>
      <rect x="30" y="15" width="74" height="48" rx="8" style={paint("soft", "none", dashed)} />
      <rect x="18" y="25" width="76" height="50" rx="8" style={paint("ink", "paper")} />
      <circle cx="35" cy="42" r="8" style={paint("accent", "tint")} />
      <path d="M32 42 L34.5 44.5 L38.5 40" style={paint("accent")} />
      <path d="M50 38.5 H80 M50 46 H70" style={paint("soft")} />
      {dots.map((x) => (
        <circle key={x} cx={x} cy="62" r="1.5" style={paint("none", "ink")} />
      ))}
      <path d="M76 62 H84" style={paint("soft")} />
    </Frame>
  );
}

/** No activity: a panel with a flat trace that blips once and goes quiet. */
export function PulseArt({ className }: IllustrationProps) {
  return (
    <Frame className={className}>
      <rect x="14" y="18" width="92" height="58" rx="9" style={paint("ink", "paper")} />
      <path d="M24 29 H42 M24 34 H34" style={paint("soft")} />
      <path d="M22 62 H96" style={paint("soft", "none", dashed)} />
      <path d="M22 52 H44 L48.5 42 L54 62 L58.5 48 L62 52 H86" style={paint("ink")} />
      <circle cx="91" cy="52" r="6" style={paint("none", "tint")} />
      <circle cx="91" cy="52" r="2.25" style={paint("accent", "accent")} />
    </Frame>
  );
}

/** No runs: a blank spec sheet with a spark at its corner. */
export function SpecSheetArt({ className }: IllustrationProps) {
  return (
    <Frame className={className}>
      <path d="M34 18 a5 5 0 0 1 5 -5 H70 L84 27 V75 a5 5 0 0 1 -5 5 H39 a5 5 0 0 1 -5 -5 Z" style={paint("ink", "paper")} />
      <path d="M70 13 V22 a5 5 0 0 0 5 5 H84" style={paint("ink")} />
      <path d="M42 33 H62" style={paint("ink")} />
      <path d="M42 43 H76 M42 51 H72 M42 59 H66" style={paint("soft", "none", dashed)} />
      <path d="M92 9 C93 15.5 94.5 17 101 18 C94.5 19 93 20.5 92 27 C91 20.5 89.5 19 83 18 C89.5 17 91 15.5 92 9 Z" style={paint("accent", "tint")} />
      <path d="M101 33 C101.4 35.6 102.4 36.6 105 37 C102.4 37.4 101.4 38.4 101 41 C100.6 38.4 99.6 37.4 97 37 C99.6 36.6 100.6 35.6 101 33 Z" style={paint("accent")} />
    </Frame>
  );
}

/** No search results: a magnifier over a grid of empty cells. */
export function SearchArt({ className }: IllustrationProps) {
  const cells = [22, 44, 66].flatMap((x) => [20, 42].map((y) => [x, y] as const));
  return (
    <Frame className={className}>
      {cells.map(([x, y]) => (
        <rect key={`${x}-${y}`} x={x} y={y} width="16" height="16" rx="4" style={paint("soft", "none", dashed)} />
      ))}
      <path d="M81.5 61.5 L95 75" style={paint("ink", "none", { strokeWidth: 4.5 })} />
      <circle cx="70" cy="50" r="16" style={paint("ink", "paper")} />
      <circle cx="70" cy="50" r="12" style={paint("none", "tint")} />
      <path d="M64.5 50 H75.5" style={paint("accent")} />
      <path d="M61 43 A11 11 0 0 1 66 39.5" style={paint("soft")} />
    </Frame>
  );
}

/** No webhooks or egress: a plug pulled out of its socket. */
export function UnpluggedArt({ className }: IllustrationProps) {
  return (
    <Frame className={className}>
      <path d="M10 70 C26 70 28 50 44 50" style={paint("ink")} />
      <rect x="44" y="41" width="16" height="18" rx="3.5" style={paint("ink", "paper")} />
      <path d="M60 46 H66 M60 54 H66" style={paint("ink")} />
      <rect x="80" y="33" width="26" height="34" rx="7" style={paint("accent", "tint")} />
      <rect x="87" y="44" width="5" height="2.5" rx="1.25" style={paint("none", "accent")} />
      <rect x="87" y="52" width="5" height="2.5" rx="1.25" style={paint("none", "accent")} />
      <path d="M71 37 L73 33 M74.5 41 L78 39.5 M71 63 L73 67 M74.5 59 L78 60.5" style={paint("soft")} />
    </Frame>
  );
}

/** All good: a key with a check badge. */
export function KeyCheckArt({ className }: IllustrationProps) {
  return (
    <Frame className={className}>
      <path d="M52 52 H96 M86 52 V60 M92 52 V58" style={paint("ink")} />
      <circle cx="38" cy="52" r="14" style={paint("ink", "paper")} />
      <circle cx="38" cy="52" r="4" style={paint("soft")} />
      <circle cx="84" cy="30" r="11" style={paint("accent", "tint")} />
      <path d="M79 30.5 L82.5 34 L89 27" style={paint("accent")} />
    </Frame>
  );
}

/** 404: a dashed path that breaks off, a dropped key in the gap, a destination that is not there. */
export function LostPathArt({ className }: IllustrationProps) {
  return (
    <Frame className={className} ground={false}>
      <path d="M12 80 C30 80 32 62 50 62" style={paint("ink", "none", dashed)} />
      <path d="M72 50 C86 50 86 38 98 38" style={paint("soft", "none", dashed)} />
      <path d="M100 33 C94 27 92 24.5 92 21 a8 8 0 0 1 16 0 c0 3.5 -2 6 -8 12 Z" style={paint("soft", "none", dashed)} />
      <circle cx="100" cy="21" r="2.5" style={paint("soft")} />
      <g transform="translate(56 74) rotate(-24)">
        <path d="M6 0 H22 M18 0 V4 M21.5 0 V3" style={paint("accent")} />
        <circle cx="0" cy="0" r="6" style={paint("accent", "tint")} />
        <circle cx="0" cy="0" r="1.75" style={paint("accent")} />
      </g>
      <ellipse cx="66" cy="84" rx="16" ry="2.5" style={{ fill: "var(--color-text-primary)", fillOpacity: 0.05 }} />
    </Frame>
  );
}

/** Just you so far: one person in accent, two empty seats beside them. */
export function PeopleArt({ className }: IllustrationProps) {
  return (
    <Frame className={className}>
      <circle cx="30" cy="45" r="7" style={paint("soft", "none", dashed)} />
      <path d="M18 70 C18 61 23.5 57 30 57 C36.5 57 42 61 42 70" style={paint("soft", "none", dashed)} />
      <circle cx="90" cy="45" r="7" style={paint("soft", "none", dashed)} />
      <path d="M78 70 C78 61 83.5 57 90 57 C96.5 57 102 61 102 70" style={paint("soft", "none", dashed)} />
      <circle cx="60" cy="36" r="10" style={paint("accent", "tint")} />
      <path d="M43 72 C43 60 50.5 53 60 53 C69.5 53 77 60 77 72" style={paint("ink", "paper")} />
    </Frame>
  );
}

/** No service groups: one tile hands off to the next along a dashed fallback arrow. */
export function FallbackArt({ className }: IllustrationProps) {
  return (
    <Frame className={className}>
      <rect x="14" y="40" width="26" height="26" rx="6" style={paint("ink", "paper")} />
      <path d="M21 50 H33 M21 56 H29" style={paint("soft")} />
      <rect x="80" y="40" width="26" height="26" rx="6" style={paint("soft", "none", dashed)} />
      <path d="M40 46 C52 26 68 26 79 44" style={paint("accent", "none", dashed)} />
      <path d="M73.5 42.5 L79.5 45 L80.5 38.5" style={paint("accent")} />
      <path d="M44 72 H76" style={paint("soft", "none", dashed)} />
    </Frame>
  );
}
