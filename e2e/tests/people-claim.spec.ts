import { expect, test, type APIRequestContext } from "@playwright/test";
import { startServer, stopServer, type Started } from "../support/server";

// Sharing with a person on a deploy locked by an access password: the claim
// link and the My access page must work for someone who never unlocks the
// site, while the dashboard stays locked. Fake password and fake key only.
const PASSWORD = "fake-e2e-people-password";
const ADMIN = { email: "people@acnologia.dev", password: "supersecret1" };

let server: Started | undefined;

test.use({ storageState: { cookies: [], origins: [] } });

test.beforeAll(async ({}, info) => {
  if (info.project.name !== "desktop-light") return;
  server = await startServer({
    ACNOLOGIA_ACCESS_PASSWORD: PASSWORD,
    ACNOLOGIA_REQUIRE_ACCESS_PASSWORD: "true",
    ACNOLOGIA_ACCESS_SIGNS_IN: "false",
    ACNOLOGIA_BOOTSTRAP_EMAIL: ADMIN.email,
    ACNOLOGIA_BOOTSTRAP_PASSWORD: ADMIN.password,
  });
});

test.afterAll(() => {
  if (server) stopServer(server.pid, server.dataDir);
  server = undefined;
});

async function admin(request: APIRequestContext, base: string, method: "GET" | "POST", path: string, data?: unknown) {
  const res = await request.fetch(`${base}${path}`, {
    method,
    data,
    headers: { "X-Acnologia-Access": PASSWORD, "Content-Type": "application/json" },
  });
  expect(res.ok(), `${method} ${path}: ${res.status()} ${await res.text()}`).toBeTruthy();
  return res.json();
}

test("sharing with a person: claim and My access work on a locked deploy", async ({ page, request, playwright }, info) => {
  test.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
  const base = server!.baseURL;

  // The owner signs in through the lock and shares one service with a person.
  await admin(request, base, "POST", "/api/auth/login", ADMIN);
  const svc = await admin(request, base, "POST", "/api/admin/services", { provider: "serpapi" });
  const serviceId = svc.id ?? svc.service?.id;
  await admin(request, base, "POST", `/api/admin/services/${serviceId}/keys`, {
    keys: [{ secret: "fake-serpapi-key-for-e2e-0001", label: "e2e key" }],
  });
  const created = await admin(request, base, "POST", "/api/admin/shares", {
    recipient: "person",
    label: "Contractor",
    name: "Contractor",
    mode: "proxy",
    services: [svc.slug ?? svc.service?.slug ?? "serpapi"],
    providers: [],
    claim_valid: "24h",
  });
  const claimURL = new URL(created.claim_url);
  expect(claimURL.pathname).toMatch(/^\/claim\/acnclaim_/);

  // A stranger with no cookies: the dashboard is locked...
  const stranger = await playwright.request.newContext();
  const locked = await stranger.get(`${base}/api/admin/shares`);
  expect(locked.status()).toBe(401);

  // ...but the claim page opens without the lock screen.
  await page.goto(`${base}${claimURL.pathname}`);
  await expect(page.getByRole("heading", { name: "This instance is private" })).toHaveCount(0);
  const get = page.getByRole("button", { name: "Get my access" });
  await expect(get).toBeVisible();
  await get.click();
  await expect(page.getByText("This is shown once. Save it now.")).toBeVisible();

  // The token is on the page once; read it from the page text.
  const body = await page.locator("body").innerText();
  const token = body.match(/acn_[A-Za-z0-9_-]{20,}/)?.[0];
  expect(token, "token shown on the claim page").toBeTruthy();

  // The link is used up.
  const again = await stranger.post(`${base}/api/claim/v1/redeem`, { data: { code: claimURL.pathname.split("/").pop() } });
  expect(again.status()).toBe(409);

  // The token works through the lock for the person's own calls...
  const me = await stranger.get(`${base}/api/v1/me`, { headers: { Authorization: `Bearer ${token}` } });
  expect(me.status()).toBe(200);
  const meBody = await me.json();
  expect(meBody.share?.label).toBe("Contractor");
  expect(meBody.share?.status).toBe("active");
  // ...and not for the dashboard.
  const adminWithToken = await stranger.get(`${base}/api/admin/shares`, { headers: { Authorization: `Bearer ${token}` } });
  expect(adminWithToken.status()).toBe(401);

  // The My access page, also without unlocking.
  await page.goto(`${base}/me`);
  await expect(page.getByRole("heading", { name: "This instance is private" })).toHaveCount(0);
  await page.getByLabel("Your token").fill(token!);
  await page.getByRole("button", { name: "Show my access" }).click();
  await expect(page.getByText("Contractor").first()).toBeVisible();

  // Revoke: the next call is refused and My access says so.
  await admin(request, base, "POST", `/api/admin/shares/${created.share.id}/revoke`);
  const after = await stranger.get(`${base}/api/v1/me`, { headers: { Authorization: `Bearer ${token}` } });
  expect((await after.json()).share?.status).toBe("revoked");
  const proxied = await stranger.get(`${base}/proxy/${svc.slug ?? "serpapi"}/search.json?q=x`, { headers: { Authorization: `Bearer ${token}` } });
  expect(proxied.status()).toBe(403);
  await stranger.dispose();
});
