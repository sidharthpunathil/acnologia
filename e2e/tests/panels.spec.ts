import type { Locator, Page } from "@playwright/test";
import { expect, test } from "../support/fixtures";
import { seedPanels, signIn, startWorkbench, type PanelSeed, type Workbench } from "../support/workbench";

// Panels the main suite's pages do not reach: Try it with an answer, the
// trash with items, the version card, an app's access controls and tokens,
// MCP sign ins and the consent page, OAuth accounts and an upstream MCP
// server's tools. Each project gets a workbench server of its own (see
// support/workbench.ts), so nothing here shows up in the main baselines.

test.use({ storageState: { cookies: [], origins: [] } });

/** Waits for a dialog (a bottom sheet on phones) to finish sliding in. */
async function settledDialog(page: Page): Promise<Locator> {
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(page.locator("[data-entering]")).toHaveCount(0);
  await expect
    .poll(async () => JSON.stringify(await dialog.boundingBox()), { intervals: [150, 150, 150] })
    .toBe(JSON.stringify(await dialog.boundingBox()));
  return dialog;
}

/** A viewport tall enough that a long dialog shows whole, without its inner scroll. */
async function tallViewport(page: Page): Promise<void> {
  const { width } = page.viewportSize()!;
  await page.setViewportSize({ width, height: 2600 });
}

test.describe("panels", () => {
  test.describe.configure({ mode: "default" });
  let w: Workbench | undefined;
  let seed: PanelSeed;

  test.beforeAll(async () => {
    w = await startWorkbench();
    try {
      seed = await seedPanels(w);
    } catch (e) {
      throw new Error(`${(e as Error).message}\nServer log:\n${w.log()}`);
    }
  });

  test.afterAll(() => {
    w?.close();
    w = undefined;
  });

  test.beforeEach(async ({ page }) => {
    await signIn(page, w!);
  });

  test("settings trash and version", async ({ page, snap }) => {
    // The newest release comes from GitHub and the upgrade command depends on
    // whether the server runs in a container: pin both, with an update waiting.
    await page.route("**/api/admin/version", async (route) => {
      const res = await route.fetch();
      const body = (await res.json()) as Record<string, unknown>;
      await route.fulfill({
        response: res,
        json: {
          ...body,
          current: "v1.4.2",
          latest: "v1.5.0",
          release_url: "https://github.com/example/releases/v1.5.0",
          update_available: true,
          upgrade_command: "docker compose pull && docker compose up -d",
        },
      });
    });
    await page.goto(`${w!.base}/settings#trash`);
    const trash = page.locator("section#trash");
    await expect(trash.getByText("Old scraper")).toBeVisible();
    await expect(trash.getByText("brave (1 key)")).toBeVisible();
    await snap("settings-trash", { target: trash });
    const backup = page.locator("section#backup");
    await expect(backup.getByText("Update available")).toBeVisible();
    await snap("settings-backup", { target: backup });
  });

  test("try it shows the answer", async ({ page, snap }) => {
    await page.goto(`${w!.base}/services/${seed.serpapi}`);
    await page.getByRole("tab", { name: /^Integrate/ }).click();
    const panel = page.locator('section[aria-labelledby="try-it-title"]');
    await expect(panel).toBeVisible();
    await page.waitForLoadState("networkidle");
    await panel.getByLabel("Path").fill("/search.json?engine=google&q=coffee");
    // A press can land while the page is still settling and do nothing; send
    // again until an answer shows (a second request changes nothing shown).
    await expect(async () => {
      await panel.getByRole("button", { name: "Send", exact: true }).click();
      await expect(panel.getByText(/ ms, as Try it$/)).toBeVisible({ timeout: 3000 });
    }).toPass({ timeout: 20_000 });
    await expect(panel.getByText('"organic_results"')).toBeVisible();
    // The latency changes every run.
    await snap("try-it-result", { target: panel, mask: [panel.getByText(/\d+ ms, as /)] });
  });

  test("app access panel", async ({ page, snap }) => {
    await tallViewport(page);
    await page.goto(`${w!.base}/apps`);
    await page.getByRole("button", { name: `Access controls for ${seed.app.name}` }).click();
    const dialog = await settledDialog(page);
    await expect(dialog.getByRole("cell", { name: "customer=cust_A" })).toBeVisible();
    await expect(dialog.getByText("No account changes")).toBeVisible();
    await snap("app-access", { target: dialog });
  });

  test("app tokens with an MCP sign in, and a token's limits", async ({ page, snap }) => {
    await tallViewport(page);
    await page.goto(`${w!.base}/apps`);
    await page.getByRole("button", { name: `Tokens for ${seed.app.name}` }).click();
    let dialog = await settledDialog(page);
    await expect(dialog.getByRole("listitem", { name: "Token eu-workers" })).toBeVisible();
    await expect(dialog.getByRole("region", { name: "MCP sign ins" }).getByText("Claude")).toBeVisible();
    // Each token's prefix is random.
    await snap("app-tokens", { target: dialog, mask: [dialog.locator('span[dir="ltr"]').filter({ hasText: /^acn_/ })] });

    await dialog.getByRole("button", { name: "Limits and tags for eu-workers" }).click();
    dialog = await settledDialog(page);
    await expect(dialog.getByText("Limits for this token")).toBeVisible();
    await snap("app-token-limits", { target: dialog });
  });

  test("account lists control MCP sign ins", async ({ page, snap }) => {
    await page.goto(`${w!.base}/account`);
    await expect(page.getByText("Control MCP sign ins")).toBeVisible();
    await expect(page.getByText("Cursor")).toBeVisible();
    await snap("account-control-sign-ins");
  });

  test("consent page for an app", async ({ page, snap }) => {
    await page.goto(`${w!.base}/oauth/consent?request=${encodeURIComponent(seed.consent.app)}`);
    await expect(page.getByRole("button", { name: "Approve" })).toBeVisible();
    await snap("oauth-consent-app");
  });

  test("consent page for the control MCP", async ({ page, snap }) => {
    await page.goto(`${w!.base}/oauth/consent?request=${encodeURIComponent(seed.consent.control)}`);
    await expect(page.getByRole("button", { name: "Approve" })).toBeVisible();
    await snap("oauth-consent-control");
  });

  test("google service with its OAuth client and no account", async ({ page, snap }) => {
    await page.goto(`${w!.base}/services/${seed.google}`);
    await expect(page.getByRole("heading", { name: "Signed in accounts" })).toBeVisible();
    await snap("service-oauth-empty");

    // The OAuth app form: the redirect URI to register, and the saved client.
    const panel = page.locator('section[aria-labelledby="oauth-accounts-title"]');
    await panel.getByRole("button", { name: "OAuth app" }).click();
    await expect(panel.getByText("Redirect URI", { exact: true })).toBeVisible();
    await snap("service-oauth-client", { target: panel });
  });

  test("OAuth service with a signed in account", async ({ page, snap }) => {
    await page.goto(`${w!.base}/services/${seed.oauthDemo}`);
    await expect(page.getByText("ana@example.com").first()).toBeVisible();
    // The account's key is made at sign in; its masked suffix changes each run.
    await snap("service-oauth-account", { mask: [page.getByText(/^\.\.\.[A-Za-z0-9_-]{4}$/)] });
  });

  test("upstream MCP tools with an access override", async ({ page, snap }) => {
    await page.goto(`${w!.base}/services/${seed.docsMcp}`);
    await page.getByRole("tab", { name: /^MCP/ }).click();
    await expect(page.getByText("upstream: create_page")).toBeVisible();
    await expect(page.getByText("override", { exact: true })).toBeVisible();
    await snap("service-mcp");
  });
});
