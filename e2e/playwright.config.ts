import { defineConfig, devices } from "@playwright/test";
import type { Theme } from "./support/fixtures";

// Screenshot suite for the dashboard. The server comes from ../bin/cogito
// (run `make build` first); support/global-setup.ts starts it on a free port
// with seeded data and support/global-teardown.ts stops it.

const desktop = { viewport: { width: 1280, height: 900 }, deviceScaleFactor: 1 };
// The phone project runs in Chrome (channel below) with the iPhone 13 size,
// user agent and touch, at a 1x scale so baselines stay small. isMobile is
// left off: with it, Chrome full page captures pin fixed bars mid page and
// shift masks off their targets.
const phone = {
  userAgent: devices["iPhone 13"].userAgent,
  viewport: { width: 390, height: 844 },
  deviceScaleFactor: 1,
  hasTouch: true,
  isMobile: false,
};

const themes: Theme[] = ["light", "dark"];

export default defineConfig({
  testDir: "./tests",
  snapshotPathTemplate: "{testDir}/../__screenshots__/{projectName}/{arg}{ext}",
  globalSetup: "./support/global-setup.ts",
  globalTeardown: "./support/global-teardown.ts",
  fullyParallel: true,
  workers: process.env.CI ? 2 : 4,
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  reporter: [["list"], ["html", { open: "never" }]],
  expect: {
    timeout: 10_000,
    toHaveScreenshot: {
      // Tight, but loose enough for font anti aliasing between runs.
      maxDiffPixelRatio: 0.01,
      threshold: 0.2,
      animations: "disabled",
      caret: "hide",
      scale: "css",
      // Paints the elements support/fixtures.ts tags as volatile.
      stylePath: "./support/mask.css",
    },
  },
  use: {
    channel: "chrome",
    headless: true,
    locale: "en-US",
    timezoneId: "UTC",
    reducedMotion: "reduce",
    storageState: "./.run/admin.json",
    trace: "retain-on-failure",
  },
  projects: themes.flatMap((theme) => [
    { name: `desktop-${theme}`, use: { ...desktop, colorScheme: theme, theme } },
    { name: `phone-${theme}`, use: { ...phone, colorScheme: theme, theme } },
  ]),
});
