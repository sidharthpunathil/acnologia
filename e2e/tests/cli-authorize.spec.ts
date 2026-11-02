import type { APIRequestContext, Page } from "@playwright/test";
import { expect, test } from "../support/fixtures";
import { startServer, stopServer, type Started } from "../support/server";

// The /cli/authorize page that approves an `acn login`. Behaviour only, no
// baselines: the code is random and the request time moves. Approving mints
// tokens and audit rows, so these run against servers of their own and the
// main suite's account and audit baselines never see them.

interface DeviceStart {
  device_code: string;
  user_code: string;
  verification_uri_complete: string;
}

const ADMIN = { email: "cli@acnologia.dev", password: "supersecret1" };

async function startDevice(request: APIRequestContext, base: string, hostname: string): Promise<DeviceStart> {
  const res = await request.post(`${base}/api/cli/device`, { data: { client: "acn", hostname, os: "linux/amd64" } });
  expect(res.status()).toBe(200);
  return (await res.json()) as DeviceStart;
}

async function poll(request: APIRequestContext, base: string, deviceCode: string) {
  const res = await request.post(`${base}/api/cli/device/token`, { data: { device_code: deviceCode } });
  return { status: res.status(), body: await res.json() };
}

async function signIn(page: Page, base: string) {
  const res = await page.request.post(`${base}/api/auth/login`, { data: ADMIN });
  expect(res.status()).toBe(200);
}

test.use({ storageState: { cookies: [], origins: [] } });

// Light desktop and the dark phone: the page must work at 390px and in dark mode.
const RUNS_IN = ["desktop-light", "phone-dark"];

test.describe("cli authorize", () => {
  test.describe.configure({ mode: "serial" });
  let server: Started | undefined;

  test.beforeAll(async ({}, info) => {
    if (!RUNS_IN.includes(info.project.name)) return;
    server = await startServer({ ACNOLOGIA_BOOTSTRAP_EMAIL: ADMIN.email, ACNOLOGIA_BOOTSTRAP_PASSWORD: ADMIN.password });
  });

  test.afterAll(() => {
    if (server) stopServer(server.pid, server.dataDir);
    server = undefined;
  });

  test("cli authorize: approve shows success and the CLI gets its token", async ({ page, request, errors }, info) => {
    test.skip(!RUNS_IN.includes(info.project.name), "runs in desktop-light and phone-dark");
    const base = server!.baseURL;
    await signIn(page, base);
    const s = await startDevice(request, base, "e2e-box");
    expect(s.user_code).toMatch(/^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$/);
    expect(s.verification_uri_complete).toBe(`${base}/cli/authorize?code=${s.user_code}`);

    await page.goto(s.verification_uri_complete);
    await expect(page.getByRole("heading", { name: "Authorize the Acnologia CLI" })).toBeVisible();
    await expect(page.getByTestId("cli-code")).toHaveText(s.user_code);
    await expect(page.getByText("e2e-box")).toBeVisible();
    await expect(page.getByText("linux/amd64")).toBeVisible();
    // The superadmin may pick any scope; write is the default.
    await expect(page.getByRole("radio")).toHaveCount(3);
    await expect(page.getByRole("radio", { name: /write/ })).toBeChecked();
    await page.getByRole("radio", { name: /read/ }).check({ force: true });

    // Fits a phone: nothing scrolls sideways.
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow).toBeLessThanOrEqual(0);

    await page.getByRole("button", { name: "Approve" }).click();
    await expect(page.getByText("You can close this tab and go back to your terminal.")).toBeVisible();

    const grant = await poll(request, base, s.device_code);
    expect(grant.status).toBe(200);
    expect(grant.body.token).toMatch(/^acnadm_/);
    expect(grant.body.scope).toBe("read");
    expect(errors).toEqual([]);
  });

  test("cli authorize: deny tells the CLI", async ({ page, request }, info) => {
    test.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
    const base = server!.baseURL;
    await signIn(page, base);
    const s = await startDevice(request, base, "deny-box");
    await page.goto(`${base}/cli/authorize?code=${s.user_code}`);
    await page.getByRole("button", { name: "Deny" }).click();
    await expect(page.getByRole("heading", { name: "Sign in denied" })).toBeVisible();
    expect((await poll(request, base, s.device_code)).body.error.code).toBe("access_denied");

    // A code that does not exist says so, and offers to type another.
    await page.goto(`${base}/cli/authorize?code=BBBB-BBBB`);
    await expect(page.getByText(/No sign in is waiting for that code/)).toBeVisible();
    await expect(page.getByLabel("Code")).toBeVisible();
  });
});

// Behind the site lock: the link survives unlocking and signing in.
test.describe("cli authorize behind the access password", () => {
  const PASSWORD = "fake-e2e-cli-gate-password";
  let server: Started | undefined;

  test.beforeAll(async ({}, info) => {
    if (info.project.name !== "desktop-light") return;
    server = await startServer({
      ACNOLOGIA_ACCESS_PASSWORD: PASSWORD,
      ACNOLOGIA_ACCESS_SIGNS_IN: "false",
      ACNOLOGIA_BOOTSTRAP_EMAIL: ADMIN.email,
      ACNOLOGIA_BOOTSTRAP_PASSWORD: ADMIN.password,
    });
  });

  test.afterAll(() => {
    if (server) stopServer(server.pid, server.dataDir);
    server = undefined;
  });

  test("cli authorize: unlock and sign in return to the code", async ({ page, request }, info) => {
    test.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
    const base = server!.baseURL;
    // Create and poll need neither the password nor an account.
    const s = await startDevice(request, base, "locked-box");

    await page.goto(`${base}/cli/authorize?code=${s.user_code}`);
    await page.getByLabel("Access password").fill(PASSWORD);
    await page.getByRole("button", { name: "Unlock" }).click();
    await page.locator('input[name="email"]').fill(ADMIN.email);
    await page.locator('input[name="password"]').fill(ADMIN.password);
    await page.getByRole("button", { name: "Sign in" }).click();

    await expect(page.getByTestId("cli-code")).toHaveText(s.user_code);
    await page.getByRole("button", { name: "Approve" }).click();
    await expect(page.getByText("You can close this tab and go back to your terminal.")).toBeVisible();

    const grant = await poll(request, base, s.device_code);
    expect(grant.status).toBe(200);
    // The token alone passes the lock: no password, no cookie.
    const me = await request.get(`${base}/api/admin/me`, { headers: { Authorization: `Bearer ${grant.body.token}` } });
    expect(me.status()).toBe(200);
    expect((await me.json()).email).toBe(ADMIN.email);
  });
});
