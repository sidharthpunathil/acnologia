import { test } from "../support/fixtures";

test.use({ storageState: { cookies: [], origins: [] } });

test("page login", async ({ page, snap }) => {
  await page.goto("/login");
  await snap("login");
});
