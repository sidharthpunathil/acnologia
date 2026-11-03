import { test } from "../support/fixtures";

// One full page screenshot per dashboard route, signed in as the superadmin.
// /setup is not covered: it only renders before the first account exists.

const ROUTES: [name: string, path: string][] = [
  ["catalog", "/"],
  ["keys", "/keys"],
  ["providers", "/providers"],
  ["provider-serpapi", "/providers/serpapi"],
  ["providers-ai", "/providers/ai"],
  ["apps", "/apps"],
  ["activity", "/activity"],
  ["usage", "/usage"],
  ["settings", "/settings"],
  ["sharing", "/sharing"],
  ["users", "/users"],
  ["audit", "/audit"],
  ["account", "/account"],
  ["groups", "/groups"],
  ["webhooks", "/webhooks"],
  ["egress", "/egress"],
  ["not-found", "/this-page-does-not-exist"],
];

// Pages that print the build version, which changes with every commit.
const SHOWS_VERSION = new Set(["settings", "sharing"]);

for (const [name, path] of ROUTES) {
  test(`page ${name}`, async ({ page, snap }) => {
    await page.goto(path);
    if (!SHOWS_VERSION.has(name)) return snap(name);
    const { version } = (await (await page.request.get("/api/admin/identity")).json()) as { version: string };
    await snap(name, { mask: [page.getByText(version, { exact: true })] });
  });
}

for (const tab of ["Keys", "Activity", "Integrate", "Settings"]) {
  test(`service serpapi tab ${tab}`, async ({ page, run, snap }) => {
    await page.goto(`/services/${run.serviceIds.serpapi}`);
    await page.getByRole("tab", { name: new RegExp(`^${tab}`) }).click();
    await snap(`service-${tab.toLowerCase()}`);
  });
}
