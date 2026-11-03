import { expect, request as playwrightRequest, test, type Page } from "@playwright/test";
import { startServer, stopServer, type Started } from "../support/server";

// Each page is its own chunk of code (web/src/lazyPage.tsx). These check what
// the split must not change: who may open which page, that a navigation
// never blanks the screen while the next page's code loads, and that the
// public landing page loads none of the dashboard. They run against a server
// of their own, so the sign ins here never reach the main suite's audit log.
const SUPER = { email: "chunks@acnologia.dev", password: "supersecret1" };
const MEMBER = { email: "member@acnologia.dev", name: "Member", password: "supersecret1" };

let server: Started | undefined;

test.use({ storageState: { cookies: [], origins: [] } });

// One project is enough: this checks behaviour, not pixels.
test.beforeAll(async ({}, info) => {
  if (info.project.name !== "desktop-light") return;
  server = await startServer({ ACNOLOGIA_BOOTSTRAP_EMAIL: SUPER.email, ACNOLOGIA_BOOTSTRAP_PASSWORD: SUPER.password });
  const api = await playwrightRequest.newContext({ baseURL: server.baseURL });
  const login = await api.post("/api/auth/login", { data: SUPER });
  expect(login.ok()).toBe(true);
  const res = await api.post("/api/admin/users", { data: { ...MEMBER, role: "member" } });
  expect(res.ok(), await res.text()).toBe(true);
  await api.dispose();
});

test.afterAll(() => {
  if (server) stopServer(server.pid, server.dataDir);
  server = undefined;
});

test.beforeEach(({}, info) => {
  test.skip(info.project.name !== "desktop-light", "runs once, in desktop-light");
});

async function signIn(page: Page, who: { email: string; password: string }) {
  const res = await page.request.post(`${server!.baseURL}/api/auth/login`, { data: { email: who.email, password: who.password } });
  expect(res.ok()).toBe(true);
}

test("a member is still sent home from the pages their role cannot open", async ({ page }) => {
  const base = server!.baseURL;
  await signIn(page, MEMBER);
  for (const path of ["/users", "/audit", "/sharing", "/providers/new"]) {
    await page.goto(base + path);
    await expect(page, path).toHaveURL(`${base}/`);
    await expect(page.getByRole("heading", { level: 1 })).toContainText(/Catalog|services/);
  }
});

test("a superadmin opens those pages", async ({ page }) => {
  const base = server!.baseURL;
  await signIn(page, SUPER);
  await page.goto(`${base}/users`);
  await expect(page).toHaveURL(`${base}/users`);
  await expect(page.getByRole("heading", { level: 1, name: "Users" })).toBeVisible();
});

test("a cold load fetches the page's code before it asks who is signed in", async ({ page }) => {
  const base = server!.baseURL;
  await signIn(page, SUPER);
  const order: string[] = [];
  page.on("request", (r) => {
    const u = new URL(r.url()).pathname;
    if (/^\/assets\/Keys-[\w-]+\.js$/.test(u) || u === "/api/auth/state") order.push(u.startsWith("/api") ? "auth" : "chunk");
  });
  await page.goto(`${base}/keys`);
  await expect(page.getByRole("heading", { level: 1, name: "Keys" })).toBeVisible();
  // main.tsx renders only once the chunk is here, and the sign in check
  // starts with the first render.
  expect(order[0]).toBe("chunk");
  expect(order).toContain("auth");
});

test("a navigation keeps the current page on screen while the next page's code loads", async ({ page }) => {
  const base = server!.baseURL;
  await signIn(page, SUPER);
  await page.goto(`${base}/`);
  await expect(page.getByRole("heading", { level: 1 })).toContainText(/Catalog|services/);

  // Slow the Keys page's code down enough to see what shows meanwhile.
  await page.route(/\/assets\/Keys-[\w-]+\.js$/, async (route) => {
    await new Promise((r) => setTimeout(r, 800));
    await route.continue();
  });
  // Samples every frame. The loading spinner, or no visible page heading
  // (Suspense hides the old page with display: none), is a flash.
  await page.evaluate(() => {
    const w = window as unknown as { __flash: string[] };
    w.__flash = [];
    const tick = () => {
      if (document.querySelector('[aria-label="Loading Acnologia"]')) w.__flash.push("spinner");
      if (![...document.querySelectorAll("h1")].some((h) => h.checkVisibility())) w.__flash.push("no visible heading");
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
  await page.getByRole("banner").getByRole("link", { name: "Keys", exact: true }).click();
  await expect(page).toHaveURL(`${base}/keys`);
  await expect(page.getByRole("heading", { level: 1, name: "Keys" })).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __flash: string[] }).__flash)).toEqual([]);
});

test("a page whose code fails to load reloads once, then says a new version is out", async ({ page }) => {
  const base = server!.baseURL;
  await signIn(page, SUPER);
  await page.goto(`${base}/`);
  await expect(page.getByRole("heading", { level: 1 })).toContainText(/Catalog|services/);

  // As after a deploy: the old build's chunk name is gone from the server.
  await page.route(/\/assets\/Assistant-[\w-]+\.js$/, (route) => route.fulfill({ status: 404, body: "" }));
  let loads = 0;
  page.on("load", () => loads++);
  await page.getByRole("banner").getByRole("link", { name: "Assistant", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Acnologia was updated" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reload" })).toBeVisible();
  // Exactly one automatic reload: the marker stops a loop.
  expect(loads).toBe(1);
  await expect(page).toHaveURL(/\/assistant/);
});

test("the landing page loads none of the dashboard's code, no Inter and no large images", async ({ page }) => {
  const base = server!.baseURL;
  const urls: string[] = [];
  page.on("request", (r) => urls.push(new URL(r.url()).pathname));
  await page.route("**/api/public/github*", (r) => r.fulfill({ json: { stars: 1 } }));
  await page.goto(`${base}/landing`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Share your API keys without giving them away.");
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  await page.waitForLoadState("networkidle");

  expect(urls.filter((u) => /^\/assets\/(Catalog|Keys|ServicePage|Usage|Markdown)-/.test(u))).toEqual([]);
  expect(urls.filter((u) => /inter-.*\.woff2$/.test(u))).toEqual([]);
  expect(urls).not.toContain("/mark.png");
  expect(urls.some((u) => /^\/mark-\d+\.webp$/.test(u))).toBe(true);
  // The headline font is asked for up front, and is the file the stylesheet uses.
  const preload = await page.locator('link[rel="preload"][as="font"]').getAttribute("href");
  expect(preload).toMatch(/^\/assets\/familjen-grotesk-latin-wght-normal-[\w-]+\.woff2$/);
  expect(urls.filter((u) => u === preload)).toHaveLength(1);
  // The poster is the small webp, and loads.
  const poster = await page.locator("video").getAttribute("poster");
  expect(poster).toBe("/video/poster.webp");
  expect((await page.request.get(base + poster)).status()).toBe(200);
});

test("public pages still render before the sign in check", async ({ page }) => {
  const base = server!.baseURL;
  await page.goto(`${base}/me`);
  await expect(page).toHaveURL(`${base}/me`);
  await page.goto(`${base}/claim/not-a-real-code`);
  await expect(page).toHaveURL(`${base}/claim/not-a-real-code`);
  await expect(page.getByRole("heading", { name: "Sign in" })).toHaveCount(0);
});
