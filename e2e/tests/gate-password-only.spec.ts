import { expect, test } from "@playwright/test";
import { startServer, stopServer, type Started } from "../support/server";

// Password only sign in (the default with an access password): the lock
// screen is the whole login, with no email form behind it.
const PASSWORD = "fake-e2e-solo-password";

let server: Started | undefined;

test.use({ storageState: { cookies: [], origins: [] } });

test.beforeAll(async ({}, info) => {
  if (info.project.name !== "desktop-light") return;
  server = await startServer({
    ACNOLOGIA_ACCESS_PASSWORD: PASSWORD,
    ACNOLOGIA_REQUIRE_ACCESS_PASSWORD: "true",
    ACNOLOGIA_BOOTSTRAP_EMAIL: "solo@acnologia.dev",
    ACNOLOGIA_BOOTSTRAP_PASSWORD: "supersecret1",
  });
});

test.afterAll(() => {
  if (server) stopServer(server.pid, server.dataDir);
  server = undefined;
});

test("password only: unlock goes straight to the dashboard", async ({ page }, info) => {
  test.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
  const base = server!.baseURL;

  await page.goto(`${base}/keys`);
  await expect(page.getByText("Enter the access password to open Acnologia.")).toBeVisible();
  await page.getByLabel("Access password").fill(PASSWORD);
  await page.getByRole("button", { name: "Unlock" }).click();

  // No sign in form: the dashboard, signed in as the owner.
  await expect(page.getByRole("heading", { name: "Sign in" })).toHaveCount(0);
  await expect(page.getByLabel("Email")).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`${base}/keys`));
  await expect(page.getByRole("heading", { name: "Keys", exact: true })).toBeVisible();
  const names = (await page.context().cookies(base)).map((c) => c.name);
  expect(names).toContain("acnologia_session");

  // Reloading keeps you in.
  await page.reload();
  await expect(page.getByRole("heading", { name: "Keys", exact: true })).toBeVisible();
});
