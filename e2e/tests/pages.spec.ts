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
  ["users", "/users"],
  ["audit", "/audit"],
  ["account", "/account"],
  ["groups", "/groups"],
  ["webhooks", "/webhooks"],
  ["egress", "/egress"],
  ["not-found", "/this-page-does-not-exist"],
];

for (const [name, path] of ROUTES) {
  test(`page ${name}`, async ({ page, snap }) => {
    await page.goto(path);
    await snap(name);
  });
}

for (const tab of ["Keys", "Activity", "Integrate", "Settings"]) {
  test(`service serpapi tab ${tab}`, async ({ page, run, snap }) => {
    await page.goto(`/services/${run.serviceIds.serpapi}`);
    await page.getByRole("tab", { name: new RegExp(`^${tab}`) }).click();
    await snap(`service-${tab.toLowerCase()}`);
  });
}
