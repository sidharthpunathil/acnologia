import { expect, test } from "@playwright/test";
import { startServer, stopServer, type Started } from "../support/server";

// An app with several tokens: add a second one, see it listed, revoke it.
// It runs against its own bin/acnologia, so the main suite's seeded apps and
// their baselines stay as they are.
const ADMIN = { email: "tokens@acnologia.dev", password: "supersecret1" };

let server: Started | undefined;

test.use({ storageState: { cookies: [], origins: [] } });

test.beforeAll(async ({}, info) => {
  if (info.project.name !== "desktop-light") return;
  server = await startServer({
    ACNOLOGIA_BOOTSTRAP_EMAIL: ADMIN.email,
    ACNOLOGIA_BOOTSTRAP_PASSWORD: ADMIN.password,
  });
});

test.afterAll(() => {
  if (server) stopServer(server.pid, server.dataDir);
  server = undefined;
});

test("app tokens: create a second token, then revoke it", async ({ page }, info) => {
  test.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
  const base = server!.baseURL;

  await page.goto(`${base}/apps`);
  await page.locator('input[name="email"]').fill(ADMIN.email);
  await page.locator('input[name="password"]').fill(ADMIN.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("button", { name: /Account menu/ })).toBeVisible();
  await page.goto(`${base}/apps`);
  await expect(page.getByRole("heading", { name: "Apps", level: 1 })).toBeVisible();

  // A new app gets its first token, named Default.
  await page.getByRole("button", { name: "Create app" }).first().click();
  await page.getByLabel("Name").fill("Billing worker");
  await page.getByRole("dialog").getByRole("button", { name: "Create app" }).click();
  await expect(page.getByRole("heading", { name: "Billing worker created" })).toBeVisible();
  await page.getByRole("button", { name: "I have saved it" }).click();
  await expect(page.getByText("1 active token")).toBeVisible();

  // Add a second token.
  await page.getByRole("button", { name: "Tokens for Billing worker" }).click();
  const list = page.getByRole("list", { name: "Tokens for Billing worker" });
  await expect(list.getByRole("listitem", { name: "Token Default" })).toBeVisible();
  await page.getByRole("button", { name: "New token" }).click();
  await page.getByLabel("Token name").fill("staging");
  await page.getByRole("button", { name: "Create token" }).click();
  await expect(page.getByText("You will not see it again")).toBeVisible();
  await page.getByRole("button", { name: "I have saved it" }).click();

  const staging = list.getByRole("listitem", { name: "Token staging" });
  await expect(staging).toBeVisible();
  await expect(staging).toContainText("used never");
  await expect(page.getByRole("dialog").getByText("2 active tokens", { exact: true })).toBeVisible();

  // Revoke it: it stays listed, marked revoked, and Default keeps working.
  await staging.getByRole("button", { name: "Revoke" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Revoke", exact: true }).last().click();
  await expect(staging.getByText("revoked", { exact: true })).toBeVisible();
  await expect(staging.getByRole("button", { name: "Revoke" })).toHaveCount(0);
  await expect(list.getByRole("listitem", { name: "Token Default" }).getByRole("button", { name: "Revoke" })).toBeVisible();
  await expect(page.getByRole("dialog").getByText("1 active token", { exact: true })).toBeVisible();
});
