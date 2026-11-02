import { expect, test } from "../support/fixtures";

// The Acnologia mark and name in the top bar always take you to the home
// page (the catalog), from the dashboard and from the docs. Behaviour only.

test("the brand in the top bar goes home", async ({ page, errors }) => {
  await page.goto("/assistant");
  await page.getByRole("link", { name: "Acnologia home" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toContainText(/Catalog|services/);
  expect(errors).toEqual([]);
});

test("the brand on a docs page goes home, and Docs goes to the docs index", async ({ page, errors }, info) => {
  await page.goto("/docs/flows");
  await page.getByRole("link", { name: "Acnologia home" }).click();
  await expect(page).toHaveURL(/\/$/);

  // The Docs label is hidden on phones, where the menu drawer leads to the index.
  if (!info.project.name.startsWith("phone")) {
    await page.goto("/docs/flows");
    await page.getByRole("banner").getByRole("link", { name: "Docs", exact: true }).click();
    await expect(page).toHaveURL(/\/docs$/);
  }
  expect(errors).toEqual([]);
});
