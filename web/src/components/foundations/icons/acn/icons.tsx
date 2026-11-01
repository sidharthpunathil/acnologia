import { createIcon } from "./base";

/*
 * Acnologia's own icons, for the product's concepts. One visual language:
 * 24 grid, 1.75 stroke, round caps and joins, 2px clear of every edge, no
 * fills except the odd solid dot. Remix stays the set for generic UI (arrows,
 * close, edit, delete, chevrons).
 */

/** Four point spark centered on (cx, cy), r from center to tip. */
function spark(cx: number, cy: number, r: number) {
  const k = r * 0.16;
  const p = (n: number) => +n.toFixed(2);
  return [
    `M${p(cx)} ${p(cy - r)}`,
    `Q${p(cx + k)} ${p(cy - k)} ${p(cx + r)} ${p(cy)}`,
    `Q${p(cx + k)} ${p(cy + k)} ${p(cx)} ${p(cy + r)}`,
    `Q${p(cx - k)} ${p(cy + k)} ${p(cx - r)} ${p(cy)}`,
    `Q${p(cx - k)} ${p(cy - k)} ${p(cx)} ${p(cy - r)}Z`,
  ].join("");
}

// keys

/** One API key: a round bow and a two tooth bit. */
export const AcnApiKey = createIcon(
  "AcnApiKey",
  <>
    <circle cx="7.75" cy="12" r="4.75" />
    <path d="M12.5 12H21V16M17.25 12V15" />
    <circle cx="7.75" cy="12" r="1.25" fill="currentColor" stroke="none" />
  </>,
);

/** The key pool: several keys racked together. */
export const AcnKeyPool = createIcon(
  "AcnKeyPool",
  <>
    <circle cx="6.75" cy="7" r="3.5" />
    <path d="M10.25 7H21V10M17.5 7V9.25" />
    <circle cx="6.75" cy="7" r="0.9" fill="currentColor" stroke="none" />
    <circle cx="6.75" cy="17" r="3.5" />
    <path d="M10.25 17H21V20M17.5 17V19.25" />
    <circle cx="6.75" cy="17" r="0.9" fill="currentColor" stroke="none" />
  </>,
);

/** A leased key: a key out on a timer, back when the clock runs down. */
export const AcnKeyLease = createIcon(
  "AcnKeyLease",
  <>
    <circle cx="16" cy="8" r="5" />
    <path d="M16 5.75V8L17.6 9.1" />
    <circle cx="6" cy="17" r="2.75" />
    <path d="M8.75 17H20.5V19.75M17 17V19" />
  </>,
);

/** Rotate keys: a key with the turn that hands traffic to the next one. */
export const AcnKeyRotate = createIcon(
  "AcnKeyRotate",
  <>
    <path d="M10.6 17.4A6 6 0 1 1 10.6 6.6" />
    <path d="M9.6 3.75L10.6 6.6L7.75 7.6" />
    <circle cx="8" cy="12" r="2.5" />
    <path d="M10.5 12H21V15M17.5 12V14.25" />
  </>,
);

/** Vault: a key held inside a shield, encrypted at rest. */
export const AcnKeyVault = createIcon(
  "AcnKeyVault",
  <>
    <path d="M12 2.75L19.25 5.5V11.25C19.25 15.9 16.3 19.4 12 21.25C7.7 19.4 4.75 15.9 4.75 11.25V5.5Z" />
    <circle cx="12" cy="9.75" r="2.1" />
    <path d="M12 11.85V16.5M12 14.5H13.9" />
  </>,
);

// traffic

/** Proxy: a request passing through the gate that adds the key. */
export const AcnProxy = createIcon(
  "AcnProxy",
  <>
    <rect x="9.25" y="3" width="5.5" height="5.25" rx="1.5" />
    <rect x="9.25" y="15.75" width="5.5" height="5.25" rx="1.5" />
    <path d="M3 12H20.5M17.25 8.75L20.5 12L17.25 15.25" />
  </>,
);

/** Egress proxy: traffic leaving for the outside world. */
export const AcnEgress = createIcon(
  "AcnEgress",
  <>
    <circle cx="9" cy="12" r="6.25" />
    <ellipse cx="9" cy="12" rx="2.5" ry="6.25" />
    <path d="M2.75 12H21M17.75 8.75L21 12L17.75 15.25" />
  </>,
);

/** Circuit breaker: an open switch between two terminals. */
export const AcnCircuitBreaker = createIcon(
  "AcnCircuitBreaker",
  <>
    <path d="M2.75 15.5H3.75M20.25 15.5H21.25" />
    <circle cx="5.75" cy="15.5" r="2" />
    <circle cx="18.25" cy="15.5" r="2" />
    <path d="M7.4 14.3L17 6" />
  </>,
);

/** Response cache: stored responses served in a flash. */
export const AcnResponseCache = createIcon(
  "AcnResponseCache",
  <>
    <ellipse cx="9" cy="6" rx="5.75" ry="2.5" />
    <path d="M3.25 6V17.5C3.25 18.9 5.8 20 9 20C10.1 20 11.15 19.87 12 19.65M14.75 6V9.5M3.25 11.75C3.25 13.15 5.8 14.25 9 14.25C9.9 14.25 10.75 14.16 11.5 14" />
    <path d="M19.25 10.5L15.5 15.75H20L16.25 21" />
  </>,
);

/** Service group: services tried in order, falling back down the chain. */
export const AcnServiceGroup = createIcon(
  "AcnServiceGroup",
  <>
    <rect x="3" y="3" width="7" height="7" rx="2" />
    <rect x="14" y="3" width="7" height="7" rx="2" />
    <rect x="14" y="14" width="7" height="7" rx="2" />
    <path d="M10 6.5H14M6.5 10V15.5C6.5 16.6 7.4 17.5 8.5 17.5H14" />
  </>,
);

/** Quota: a gauge of how much of the allowance is used. */
export const AcnQuota = createIcon(
  "AcnQuota",
  <>
    <path d="M5.1 18.5A8.5 8.5 0 1 1 18.9 18.5" />
    <path d="M12 14L15.5 9.5" />
    <circle cx="12" cy="14" r="1.6" fill="currentColor" stroke="none" />
  </>,
);

/** Rate limited: requests stopped at a wall until the window opens again. */
export const AcnRateLimited = createIcon(
  "AcnRateLimited",
  <>
    <path d="M20 3.5V20.5" />
    <path d="M3.5 12H15.5M12.25 8.75L15.5 12L12.25 15.25" />
    <path d="M5.5 7H9.5M5.5 17H9.5" />
  </>,
);

// providers and apps

/** Provider spec: the sheet that describes a provider's API. */
export const AcnProviderSpec = createIcon(
  "AcnProviderSpec",
  <>
    <path d="M17 21H7C5.9 21 5 20.1 5 19V5C5 3.9 5.9 3 7 3H13.5L19 8.5V19C19 20.1 18.1 21 17 21Z" />
    <path d="M13.5 3V7C13.5 7.8 14.2 8.5 15 8.5H19" />
    <path d="M10 11.5L8 14L10 16.5M14 11.5L16 14L14 16.5" />
  </>,
);

/** AI provider builder: a spec sheet the AI is writing. */
export const AcnAiBuilder = createIcon(
  "AcnAiBuilder",
  <>
    <path d="M11 21H7C5.9 21 5 20.1 5 19V5C5 3.9 5.9 3 7 3H13.5L17.5 7V9.5" />
    <path d="M8.5 9H12M8.5 12.5H10.5" />
    <path d={spark(17, 16.5, 4.5)} />
  </>,
);

/** App and its token: an app tile holding its own access token. */
export const AcnAppToken = createIcon(
  "AcnAppToken",
  <>
    <path d="M14.5 10.25V6.5C14.5 4.85 13.15 3.5 11.5 3.5H6.5C4.85 3.5 3.5 4.85 3.5 6.5V11.5C3.5 13.15 4.85 14.5 6.5 14.5H10.25" />
    <circle cx="16.5" cy="16.5" r="4.5" />
    <circle cx="16.5" cy="16.5" r="1.25" fill="currentColor" stroke="none" />
  </>,
);

// categories

/** LLM category: a reply bubble with a spark. */
export const AcnCategoryLlm = createIcon(
  "AcnCategoryLlm",
  <>
    <path d="M5 4H19C20.1 4 21 4.9 21 6V15C21 16.1 20.1 17 19 17H10.5L6.5 20.25V17H5C3.9 17 3 16.1 3 15V6C3 4.9 3.9 4 5 4Z" />
    <path d={spark(12, 10.5, 3.75)} />
  </>,
);

/** Search category: a lens over results. */
export const AcnCategorySearch = createIcon(
  "AcnCategorySearch",
  <>
    <circle cx="10.5" cy="10.5" r="6.75" />
    <path d="M15.5 15.5L20.75 20.75" />
    <path d="M7.5 9.5C7.85 8.4 8.65 7.6 9.75 7.3" />
  </>,
);

/** Voice category: a sound wave. */
export const AcnCategoryVoice = createIcon(
  "AcnCategoryVoice",
  <>
    <path d="M4 10V14M8 6.5V17.5M12 3.5V20.5M16 7.5V16.5M20 10.5V13.5" />
  </>,
);

/** Image category: a framed picture. */
export const AcnCategoryImage = createIcon(
  "AcnCategoryImage",
  <>
    <rect x="3" y="4" width="18" height="16" rx="2.5" />
    <path d="M3.5 17.5L8.75 12.25L13 16.5L15.5 14L20.5 19" />
    <circle cx="15.75" cy="8.75" r="1.75" />
  </>,
);

/** Other category: assorted shapes. */
export const AcnCategoryOther = createIcon(
  "AcnCategoryOther",
  <>
    <circle cx="7" cy="7" r="3.5" />
    <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
    <path d="M12 13.75L16.5 20.5H7.5Z" />
  </>,
);
