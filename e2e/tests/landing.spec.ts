import { expect, test as plain } from "@playwright/test";
import { test } from "../support/fixtures";
import { startServer, stopServer, type Started } from "../support/server";

// The public landing page and the redirect to it. Signed out, home (/) is
// /landing and Sign in starts the usual flow; deep links into the dashboard
// still ask for the lock screen or sign in. Behaviour only: the screenshot
// is in login.spec.ts.

const REPO = "https://github.com/sidharthpunathil/acnologia";

test.describe("open deploy, signed out", () => {
  test.use({ storageState: { cookies: [], origins: [] } });

  test("home goes to the landing page, and Sign in to the sign in page", async ({ page, errors }) => {
    await page.goto("/");
    await expect(page).toHaveURL(/\/landing$/);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Share your API keys without giving them away.");
    await expect(page).toHaveTitle("Acnologia: share API keys without giving them away");

    const github = page.getByRole("main").getByRole("link", { name: /View on GitHub/ }).first();
    await expect(github).toHaveAttribute("href", REPO);
    await expect(github).toHaveAttribute("target", "_blank");
    await expect(github).toHaveAttribute("rel", "noopener noreferrer");

    // Docs are readable signed out here, so the link stays in the app.
    await expect(page.getByRole("link", { name: "Read the docs" })).toHaveAttribute("href", "/docs");

    await page.getByRole("banner").getByRole("link", { name: "Sign in" }).click();
    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
    await expect(page).toHaveURL(/\/login$/);
    expect(errors).toEqual([]);
  });

  test("the redirect replaces home in history, so Back does not loop", async ({ page }) => {
    await page.goto("/docs");
    await page.goto("/");
    await expect(page).toHaveURL(/\/landing$/);
    await page.goBack();
    await expect(page).toHaveURL(/\/docs$/);
  });

  test("a deep link still asks to sign in", async ({ page }) => {
    await page.goto("/keys");
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  });
});

test("signed in, the landing page offers the dashboard", async ({ page }) => {
  await page.goto("/landing");
  await page.getByRole("banner").getByRole("link", { name: "Open dashboard" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toContainText(/Catalog|services/);
});

plain.describe("locked deploy", () => {
  const PASSWORD = "fake-e2e-landing-password";
  const ADMIN = { email: "landing@acnologia.dev", password: "supersecret1" };
  let server: Started | undefined;

  plain.use({ storageState: { cookies: [], origins: [] } });

  plain.beforeAll(async ({}, info) => {
    if (info.project.name !== "desktop-light") return;
    server = await startServer({
      ACNOLOGIA_ACCESS_PASSWORD: PASSWORD,
      ACNOLOGIA_REQUIRE_ACCESS_PASSWORD: "true",
      ACNOLOGIA_ACCESS_SIGNS_IN: "false",
      ACNOLOGIA_BOOTSTRAP_EMAIL: ADMIN.email,
      ACNOLOGIA_BOOTSTRAP_PASSWORD: ADMIN.password,
    });
  });

  plain.afterAll(() => {
    if (server) stopServer(server.pid, server.dataDir);
    server = undefined;
  });

  plain("home is the landing page; Sign in goes through the lock screen and back home", async ({ page }, info) => {
    plain.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
    const base = server!.baseURL;
    const failed: string[] = [];
    page.on("response", (r) => {
      if (r.status() >= 400 && !r.url().includes("/api/")) failed.push(`${r.status()} ${r.url()}`);
    });
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));

    await page.goto(`${base}/`);
    await expect(page).toHaveURL(`${base}/landing`);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Share your API keys without giving them away.");
    await expect(page.getByRole("heading", { name: "This instance is private" })).toHaveCount(0);
    await expect(page.getByRole("main").getByRole("link", { name: /View on GitHub/ }).first()).toHaveAttribute("href", REPO);
    // The docs are locked here, so the link goes to the README's list.
    await expect(page.getByRole("link", { name: /Read the docs/ })).toHaveAttribute("href", `${REPO}#documentation`);
    // Every asset the page needs loads past the lock: scripts, styles, fonts, the mark.
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    const markLoaded = await page
      .locator('img[src="/mark.png"]')
      .first()
      .evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0);
    expect(markLoaded).toBe(true);
    expect(await page.evaluate(() => [...document.fonts].some((f) => f.status === "loaded"))).toBe(true);
    expect(failed).toEqual([]);
    expect(errors).toEqual([]);

    await page.getByRole("banner").getByRole("link", { name: "Sign in" }).click();
    await expect(page.getByRole("heading", { name: "This instance is private" })).toBeVisible();
    await page.getByLabel("Access password").fill(PASSWORD);
    await page.getByRole("button", { name: "Unlock" }).click();

    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
    await page.locator('input[name="email"]').fill(ADMIN.email);
    await page.locator('input[name="password"]').fill(ADMIN.password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("button", { name: /Account menu/ })).toBeVisible();
    await expect(page).toHaveURL(`${base}/`);
  });

  plain("a deep link still shows the lock screen", async ({ page }, info) => {
    plain.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
    await page.goto(`${server!.baseURL}/sharing`);
    await expect(page.getByRole("heading", { name: "This instance is private" })).toBeVisible();
    await expect(page).toHaveURL(`${server!.baseURL}/sharing`);
  });
});
