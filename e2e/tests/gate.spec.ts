import { expect, test } from "@playwright/test";
import { startServer, stopServer, type Started } from "../support/server";

// The access gate, against a second bin/acnologia started here with an
// access password. The main suite's server has none, so its pages and
// baselines are unchanged. The password is an obvious fake.
const PASSWORD = "fake-e2e-gate-password";
const ADMIN = { email: "gate@acnologia.dev", password: "supersecret1" };

let server: Started | undefined;

test.use({ storageState: { cookies: [], origins: [] } });

// One project is enough: this checks behaviour, not pixels.
test.beforeAll(async ({}, info) => {
  if (info.project.name !== "desktop-light") return;
  server = await startServer({
    ACNOLOGIA_ACCESS_PASSWORD: PASSWORD,
    ACNOLOGIA_REQUIRE_ACCESS_PASSWORD: "true",
    ACNOLOGIA_BOOTSTRAP_EMAIL: ADMIN.email,
    ACNOLOGIA_BOOTSTRAP_PASSWORD: ADMIN.password,
  });
});

test.afterAll(() => {
  if (server) stopServer(server.pid, server.dataDir);
  server = undefined;
});

test("access gate: lock screen, unlock, then sign in", async ({ page, request }, info) => {
  test.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
  const base = server!.baseURL;

  // Locked from outside: data is refused, the crawler rules are served.
  const locked = await request.get(`${base}/api/auth/state`);
  expect(locked.status()).toBe(401);
  expect((await locked.json()).error.code).toBe("locked");
  expect(locked.headers()["x-robots-tag"]).toContain("noindex");
  expect(await (await request.get(`${base}/robots.txt`)).text()).toBe("User-agent: *\nDisallow: /\n");
  expect((await request.get(`${base}/llms.txt`)).status()).toBe(401);

  // A page navigation gets the lock screen, not the sign in page.
  await page.goto(`${base}/keys`);
  await expect(page.getByRole("heading", { name: "This instance is private" })).toBeVisible();
  await expect(page.getByText("Private Acnologia instance")).toBeVisible();
  await expect(page.getByLabel("Email")).toHaveCount(0);

  const field = page.getByLabel("Access password");
  await field.fill("not-the-password");
  // The show and hide toggle reveals what was typed.
  await page.getByRole("button", { name: "Show password" }).click();
  await expect(field).toHaveAttribute("type", "text");
  await page.getByRole("button", { name: "Unlock" }).click();
  await expect(page.getByText("That password is not correct.")).toBeVisible();

  await field.fill(PASSWORD);
  await page.getByRole("button", { name: "Unlock" }).click();

  // Past the gate: the normal sign in, then the dashboard.
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  const cookies = await page.context().cookies(base);
  const gate = cookies.find((c) => c.name === "acnologia_gate");
  expect(gate?.httpOnly).toBe(true);
  expect(gate?.sameSite).toBe("Lax");
  expect(gate?.value).not.toContain(PASSWORD);

  await page.locator('input[name="email"]').fill(ADMIN.email);
  await page.locator('input[name="password"]').fill(ADMIN.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("button", { name: /Account menu/ })).toBeVisible();

  // "Lock this browser" brings the lock screen back.
  await page.getByRole("button", { name: /Account menu/ }).click();
  await page.getByRole("button", { name: "Lock this browser" }).click();
  await expect(page.getByRole("heading", { name: "This instance is private" })).toBeVisible();

  // One wrong guess was spent above; four more use up the minute, and the
  // next is refused with a countdown and a disabled button.
  const lockField = page.getByLabel("Access password");
  for (let i = 0; i < 5; i++) {
    await lockField.fill(`wrong-guess-${i}`);
    await page.getByRole("button", { name: "Unlock" }).click();
    await expect(lockField).toHaveValue("");
  }
  await expect(page.getByText("Too many wrong passwords")).toBeVisible();
  await expect(page.getByText(/Try again in 0:\d\d\./)).toBeVisible();
  await lockField.fill(PASSWORD);
  await expect(page.getByRole("button", { name: "Unlock" })).toBeDisabled();
});
