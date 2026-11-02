import { expect, test } from "../support/fixtures";
import { mockChat } from "../support/chat-mock";

test("assistant conversation", async ({ page, run, snap }) => {
  await mockChat(page, run.now);
  await page.goto("/assistant/7");
  await expect(page.getByText("Delete key", { exact: false }).first()).toBeVisible();
  await snap("assistant");
});

test("account menu open", async ({ page, snap }, info) => {
  test.skip(info.project.name.startsWith("phone"), "phones use the More sheet");
  await page.goto("/");
  // Named "Account menu: Demo", plus ", current page Usage" while one of its pages is open.
  const trigger = page.getByRole("button", { name: /^Account menu: Demo/ });
  await trigger.click();
  await expect(trigger).toHaveAttribute("aria-expanded", "true");
  await snap("account-menu", { viewportOnly: true });
});

test("more sheet open", async ({ page, snap }, info) => {
  test.skip(!info.project.name.startsWith("phone"), "desktop uses the account menu");
  await page.goto("/");
  await page.getByRole("button", { name: "More" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await snap("more-sheet", { viewportOnly: true });
});

test("add keys modal with a 50 key paste", async ({ page, run, snap }) => {
  await page.goto(`/services/${run.serviceIds.serpapi}`);
  await page.getByRole("button", { name: "Add keys" }).first().click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  const paste = Array.from({ length: 50 }, (_, i) => {
    const n = String(i + 1).padStart(2, "0");
    return `bulk-${n}, demo-serp-bulk-${n}${"x".repeat(12)}`;
  }).join("\n");
  await dialog.locator("textarea").first().fill(paste);
  await expect(dialog.getByRole("button", { name: /Add 50 keys/ })).toBeVisible();
  // On phones the dialog is a bottom sheet: wait until it has finished sliding in.
  await expect(page.locator("[data-entering]")).toHaveCount(0);
  await expect
    .poll(async () => JSON.stringify(await dialog.boundingBox()), { intervals: [150, 150, 150] })
    .toBe(JSON.stringify(await dialog.boundingBox()));
  await snap("add-keys-50", { viewportOnly: true });
});
