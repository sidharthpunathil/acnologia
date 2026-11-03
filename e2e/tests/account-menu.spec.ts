import { expect, test } from "../support/fixtures";
import type { Page } from "@playwright/test";

// The account menu groups the secondary pages: one row per group, each
// opening its pages in a flyout beside the menu (desktop), or expanding them
// in place in the More sheet (phone).

const desktopOnly = (name: string) => test.skip(name.startsWith("phone"), "phones use the More sheet");
const phoneOnly = (name: string) => test.skip(!name.startsWith("phone"), "desktop uses the account menu");

function trigger(page: Page) {
  return page.getByRole("button", { name: /^Account menu: Demo/ });
}

function accountMenu(page: Page) {
  return page.getByRole("dialog", { name: "Account menu" });
}

async function openMenu(page: Page) {
  await trigger(page).click();
  await expect(accountMenu(page)).toBeVisible();
}

test("hovering a group opens its pages beside the menu", async ({ page }, info) => {
  desktopOnly(info.project.name);
  await page.goto("/");
  await openMenu(page);
  await accountMenu(page).getByRole("menuitem", { name: /^Workspace/ }).hover();
  const flyout = page.getByRole("menu", { name: "Workspace", exact: true });
  await expect(flyout).toBeVisible();
  // The menu sits at the end of the top bar, so the flyout opens to its left.
  const panel = (await accountMenu(page).boundingBox())!;
  const sub = (await flyout.boundingBox())!;
  expect(sub.x + sub.width).toBeLessThanOrEqual(panel.x);
  await flyout.getByRole("menuitem", { name: "Usage" }).click();
  await expect(page).toHaveURL(/\/usage$/);
  await expect(accountMenu(page)).toBeHidden();
});

test("clicking a group opens its pages", async ({ page }, info) => {
  desktopOnly(info.project.name);
  await page.goto("/");
  await openMenu(page);
  const row = accountMenu(page).getByRole("menuitem", { name: /^Manage/ });
  await expect(row).toHaveAttribute("aria-haspopup", "menu");
  await row.click();
  await expect(row).toHaveAttribute("aria-expanded", "true");
  await page.getByRole("menu", { name: "Manage", exact: true }).getByRole("menuitem", { name: "Webhooks" }).click();
  await expect(page).toHaveURL(/\/webhooks$/);
});

test("keyboard opens, walks and closes the submenus", async ({ page }, info) => {
  desktopOnly(info.project.name);
  await page.goto("/");
  await trigger(page).focus();
  await page.keyboard.press("Enter");
  const workspace = accountMenu(page).getByRole("menuitem", { name: /^Workspace/ });
  const manage = accountMenu(page).getByRole("menuitem", { name: /^Manage/ });
  await expect(workspace).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(manage).toBeFocused();

  // ArrowRight opens the group and lands on its first page.
  await page.keyboard.press("ArrowRight");
  const flyout = page.getByRole("menu", { name: "Manage", exact: true });
  await expect(flyout.getByRole("menuitem", { name: "Service groups" })).toBeFocused();
  // ArrowLeft closes it and returns to the group row.
  await page.keyboard.press("ArrowLeft");
  await expect(flyout).toBeHidden();
  await expect(manage).toBeFocused();

  // Escape closes one level at a time.
  await page.keyboard.press("Enter");
  await expect(flyout.getByRole("menuitem", { name: "Service groups" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(flyout).toBeHidden();
  await expect(manage).toBeFocused();
  await expect(accountMenu(page)).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(accountMenu(page)).toBeHidden();
  await expect(trigger(page)).toBeFocused();

  // Typeahead finds a group; Enter on a page follows it.
  await page.keyboard.press("Enter");
  await expect(workspace).toBeFocused();
  await page.keyboard.type("a");
  await expect(accountMenu(page).getByRole("menuitem", { name: /^Admin/ })).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowDown");
  await expect(page.getByRole("menu", { name: "Admin", exact: true }).getByRole("menuitem", { name: "Users" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/users$/);
});

test("arrow keys run on from the pages to the account rows", async ({ page }, info) => {
  desktopOnly(info.project.name);
  await page.goto("/");
  await trigger(page).focus();
  await page.keyboard.press("Enter");
  await expect(accountMenu(page).getByRole("menuitem", { name: /^Workspace/ })).toBeFocused();
  await page.keyboard.press("End");
  await expect(accountMenu(page).getByRole("menuitem", { name: /^Help/ })).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(accountMenu(page).getByRole("menuitem", { name: "Your account" })).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(accountMenu(page).getByRole("menuitem", { name: /^Help/ })).toBeFocused();
});

test("the open page's group carries the dot", async ({ page }, info) => {
  desktopOnly(info.project.name);
  await page.goto("/usage");
  await openMenu(page);
  const workspace = accountMenu(page).getByRole("menuitem", { name: /^Workspace/ });
  await expect(workspace).toContainText("Usage");
  await expect(workspace).toHaveAccessibleName(/current page/);
  await expect(accountMenu(page).getByRole("menuitem", { name: /^Manage/ })).not.toHaveAccessibleName(/current page/);
  await workspace.click();
  const flyout = page.getByRole("menu", { name: "Workspace", exact: true });
  await expect(flyout.getByRole("menuitem", { name: /^Usage/ })).toHaveAccessibleName(/^Usage\s*, current page$/);
  await expect(flyout.getByRole("menuitem", { name: /^Apps/ })).toHaveAccessibleName("Apps");
});

test("the menu fits a 1280x720 screen without scrolling", async ({ page }, info) => {
  desktopOnly(info.project.name);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto("/");
  await openMenu(page);
  const box = (await accountMenu(page).boundingBox())!;
  expect(box.y + box.height).toBeLessThanOrEqual(720);
  const scroller = accountMenu(page).locator(".overflow-y-auto").first();
  const fits = await scroller.evaluate((el) => el.scrollHeight <= el.clientHeight);
  expect(fits).toBe(true);
  await expect(accountMenu(page).getByRole("menuitem", { name: "Sign out" })).toBeInViewport();
});

test("account submenu open", async ({ page, snap }, info) => {
  desktopOnly(info.project.name);
  await page.goto("/");
  await openMenu(page);
  await accountMenu(page).getByRole("menuitem", { name: /^Manage/ }).click();
  await expect(page.getByRole("menu", { name: "Manage", exact: true })).toBeVisible();
  await page.mouse.move(0, 0);
  await snap("account-submenu", { viewportOnly: true });
});

test("the More sheet expands groups in place", async ({ page }, info) => {
  phoneOnly(info.project.name);
  await page.goto("/usage");
  await page.getByRole("button", { name: "More" }).click();
  const sheet = page.getByRole("dialog");
  await expect(sheet).toBeVisible();
  // The open page's group starts expanded, and says which page is open.
  const workspace = sheet.getByRole("button", { name: /^Workspace/ });
  const manage = sheet.getByRole("button", { name: /^Manage/ });
  await expect(workspace).toHaveAttribute("aria-expanded", "true");
  await expect(workspace).toHaveAccessibleName(/current page/);
  await expect(manage).toHaveAttribute("aria-expanded", "false");
  const box = (await manage.boundingBox())!;
  expect(box.height).toBeGreaterThanOrEqual(44);

  // One group open at a time.
  await manage.click();
  await expect(manage).toHaveAttribute("aria-expanded", "true");
  await expect(workspace).toHaveAttribute("aria-expanded", "false");
  const webhooks = sheet.getByRole("link", { name: "Webhooks" });
  expect((await webhooks.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await webhooks.click();
  await expect(page).toHaveURL(/\/webhooks$/);
  await expect(sheet).toBeHidden();
});

test("more sheet with a group expanded", async ({ page, snap }, info) => {
  phoneOnly(info.project.name);
  await page.goto("/");
  await page.getByRole("button", { name: "More" }).click();
  const sheet = page.getByRole("dialog");
  await expect(sheet).toBeVisible();
  await sheet.getByRole("button", { name: /^Manage/ }).click();
  await expect(sheet.getByRole("link", { name: "Webhooks" })).toBeVisible();
  await expect(page.locator("[data-entering]")).toHaveCount(0);
  await snap("more-sheet-manage", { viewportOnly: true });
});
