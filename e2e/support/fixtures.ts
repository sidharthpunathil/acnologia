import { test as base, expect, type Locator, type Page } from "@playwright/test";
import fs from "node:fs";
import { STATE_FILE, type RunState } from "./paths";

export type Theme = "light" | "dark";

interface Fixtures {
  /** Server address, seed ids and the frozen clock time from global setup. */
  run: RunState;
  /** Console errors and uncaught page errors seen so far in this test. */
  errors: string[];
  /** Waits for the page to settle, checks it, and compares a full page screenshot. */
  snap: (name: string, opts?: SnapOptions) => Promise<void>;
}

export interface SnapOptions {
  /** Extra elements to paint over, on top of the automatic masks. */
  mask?: Locator[];
  /** Screenshot the viewport only (for overlays such as menus and sheets). Default is the full page. */
  viewportOnly?: boolean;
}

// Text that changes with the wall clock. Any element whose own text matches
// one of these is masked, so baselines survive a run on another day.
const MONTH = "(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)[a-z]*\\.?";
const UNIT = "(?:s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?|d|days?|w|wks?|weeks?|mo|mos|months?|y|yrs?|years?)";
const TIME_PATTERNS = [
  `\\b\\d+\\s*${UNIT}\\s+ago\\b`,
  `\\bin\\s+\\d+\\s*${UNIT}\\b`,
  `\\b\\d+\\s*${UNIT}\\s+(?:left|remaining)\\b`,
  "\\bjust now\\b",
  "\\b(?:a|an|one)\\s+(?:second|minute|hour|day|week|month|year)\\s+ago\\b",
  `\\b${MONTH}\\s+\\d{1,2}\\b`,
  `\\b\\d{1,2}\\s+${MONTH}(?:\\s|$|,)`,
  "\\b\\d{4}-\\d{2}-\\d{2}\\b",
  "\\b\\d{1,2}/\\d{1,2}/\\d{2,4}\\b",
  "\\b\\d{1,2}:\\d{2}(?::\\d{2})?\\s*(?:AM|PM)?\\b",
];

// The server and the fake upstream listen on a new port each run, and the
// dashboard prints those addresses (service base URLs, Integrate snippets).
const PORT_PATTERN = "\\b(?:127\\.0\\.0\\.1|localhost):\\d{2,5}\\b";

/**
 * Tags every element whose own text is time based or carries a per run port.
 * Time patterns skip code samples, where a date or "12:30" is static example
 * text.
 */
async function tagVolatile(page: Page): Promise<void> {
  await page.evaluate(
    ([time, port]) => {
      const timeRe = new RegExp(time.join("|"), "i");
      const portRe = new RegExp(port);
      document.querySelectorAll("[data-e2e-mask]").forEach((el) => el.removeAttribute("data-e2e-mask"));
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        const parent = n.parentElement;
        const text = n.textContent ?? "";
        if (!parent || parent.closest("script, style")) continue;
        const isTime = timeRe.test(text) && !parent.closest("pre, code");
        if (isTime || portRe.test(text)) parent.setAttribute("data-e2e-mask", "");
      }
      document.querySelectorAll("time").forEach((el) => el.setAttribute("data-e2e-mask", ""));
      // Form fields hold text in a value, not a text node.
      document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input, textarea").forEach((el) => {
        if (portRe.test(el.value)) el.setAttribute("data-e2e-mask", "");
      });
    },
    [TIME_PATTERNS, PORT_PATTERN] as const,
  );
}

/** Waits until the network is quiet, skeletons are gone and fonts are loaded. */
export async function settle(page: Page): Promise<void> {
  await page.waitForLoadState("networkidle");
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0, { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  // Masks are placed from page coordinates; start every capture at the top.
  await page.evaluate(() => window.scrollTo(0, 0));
  // Two frames, so layout from the last state change is painted.
  await page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))));
}

export const test = base.extend<Fixtures & { theme: Theme }>({
  theme: ["light", { option: true }],

  run: async ({}, use) => {
    await use(JSON.parse(fs.readFileSync(STATE_FILE, "utf8")) as RunState);
  },

  baseURL: async ({ run }, use) => {
    await use(run.baseURL);
  },

  errors: async ({}, use) => {
    await use([]);
  },

  page: async ({ page, theme, run, errors }, use) => {
    await page.addInitScript((t) => {
      try {
        localStorage.setItem("cogito.theme", t);
      } catch {
        // Storage can be blocked on about:blank; the next navigation sets it.
      }
    }, theme);
    // Date.now and new Date() return this time; timers keep running.
    await page.clock.setFixedTime(run.now);
    page.on("console", (msg) => {
      if (msg.type() === "error") errors.push(`console: ${msg.text()} (${msg.location().url})`);
    });
    page.on("pageerror", (err) => errors.push(`pageerror: ${err.message}`));
    await use(page);
  },

  snap: async ({ page, errors }, use) => {
    await use(async (name, opts = {}) => {
      await settle(page);
      expect(errors, "console or page errors").toEqual([]);
      const overflow = await page.evaluate(() => ({
        scroll: document.documentElement.scrollWidth,
        inner: window.innerWidth,
      }));
      expect(overflow.scroll, `horizontal overflow: scrollWidth ${overflow.scroll} > innerWidth ${overflow.inner}`).toBeLessThanOrEqual(
        overflow.inner,
      );
      // Full page: grow the viewport to the document height instead of using
      // fullPage, which leaves position: fixed bars (the phone tab bar) at the
      // first screen's bottom edge, on top of the content.
      const size = page.viewportSize()!;
      if (!opts.viewportOnly) {
        const height = await page.evaluate(() => document.documentElement.scrollHeight);
        if (height > size.height) {
          await page.setViewportSize({ width: size.width, height });
          await settle(page);
        }
      }
      await tagVolatile(page);
      for (const loc of opts.mask ?? []) {
        await loc.evaluateAll((els) => els.forEach((el) => el.setAttribute("data-e2e-mask", "")));
      }
      await expect(page).toHaveScreenshot(`${name}.png`);
      await page.setViewportSize(size);
    });
    // Errors that showed up after the last snapshot still fail the test.
    expect(errors, "console or page errors").toEqual([]);
  },
});

export { expect };
