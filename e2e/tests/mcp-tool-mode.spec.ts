import { expect, test } from "@playwright/test";
import { startServer, stopServer, type Started } from "../support/server";

// An app's MCP tool list mode: switch from Auto to Search, save, and see the
// listing summary change. It runs against its own bin/acnologia, so the main
// suite's seeded apps and their baselines stay as they are.
const ADMIN = { email: "mcp-mode@acnologia.dev", password: "supersecret1" };

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

test("mcp tool mode: search lists fewer tools", async ({ page }, info) => {
  test.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
  const base = server!.baseURL;

  await page.goto(`${base}/apps`);
  await page.locator('input[name="email"]').fill(ADMIN.email);
  await page.locator('input[name="password"]').fill(ADMIN.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("button", { name: /Account menu/ })).toBeVisible();

  // A service whose provider documents endpoints gives the app service tools.
  const headers = { "X-Requested-With": "e2e" };
  const svc = await page.request.post(`${base}/api/admin/services`, { headers, data: { provider: "tavily", slug: "tavily" } });
  expect(svc.ok(), await svc.text()).toBeTruthy();
  const app = await page.request.post(`${base}/api/admin/apps`, { headers, data: { name: "Agent box", mode: "both" } });
  expect(app.ok(), await app.text()).toBeTruthy();

  await page.goto(`${base}/apps`);
  await page.getByRole("button", { name: "MCP settings for Agent box" }).click();
  const dialog = page.getByRole("dialog");
  const summary = dialog.getByTestId("mcp-listing").first();

  // Auto with few service tools lists everything.
  await expect(summary).toHaveText(/^Listing all \d+ tools\.$/);
  await expect(dialog.getByText("Connected clients are told when the tool list changes")).toBeVisible();

  // The radio input is visually hidden: click the card, as a person would.
  await dialog.getByText("Search", { exact: true }).click();
  await expect(dialog.getByRole("radio", { name: /^Search/ })).toBeChecked();
  await expect(summary).toContainText("Save to update.");
  await dialog.getByRole("button", { name: "Save changes" }).click();
  await expect(summary).toHaveText(/^Listing \d+ of \d+ tools: the rest are found with search_tools\.$/);

  // The tools left out of tools/list are marked in the Tools tab.
  await dialog.getByRole("tab", { name: /Tools/ }).click();
  await expect(dialog.getByText("via search").first()).toBeVisible();
});
