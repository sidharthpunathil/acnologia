import { test } from "../support/fixtures";

test.use({ storageState: { cookies: [], origins: [] } });

test("page login", async ({ page, snap }) => {
  await page.goto("/login");
  await snap("login");
});

// The public home page, signed out on a deploy with no access password.
test("page landing", async ({ page, snap }) => {
  await page.goto("/landing");
  await snap("landing");
});
