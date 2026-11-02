import type { Page } from "@playwright/test";
import { expect, test } from "../support/fixtures";
import { mockChat } from "../support/chat-mock";

// Leaving the Assistant for another tab and coming back keeps the chat you
// had open and what you were typing. Behaviour only, no screenshots.

/** The top bar on desktop and the tab bar on phones both link the main pages. */
const navLink = (page: Page, name: string) =>
  page.getByRole("link", { name, exact: true }).filter({ visible: true }).first();

test("coming back to the Assistant reopens the last chat", async ({ page, run, errors }) => {
  await mockChat(page, run.now);
  await page.goto("/assistant/7");
  await expect(page.getByText("Delete key", { exact: false }).first()).toBeVisible();

  await navLink(page, "Catalog").click();
  await expect(page).not.toHaveURL(/\/assistant/);
  await navLink(page, "Assistant").click();

  await expect(page).toHaveURL(/\/assistant\/7$/);
  await expect(page.getByText("Delete key", { exact: false }).first()).toBeVisible();
  await expect(page.getByText("What can I help with?")).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("a half typed message survives switching tabs", async ({ page, run, errors }) => {
  await mockChat(page, run.now);
  await page.goto("/assistant/7");
  await expect(page.getByText("Delete key", { exact: false }).first()).toBeVisible();

  const box = page.locator("#assistant-input");
  await box.fill("Also check the ElevenLabs keys");
  // Phones hide the tab bar while a text field has focus.
  await box.blur();

  await navLink(page, "Catalog").click();
  await expect(page).not.toHaveURL(/\/assistant/);
  await navLink(page, "Assistant").click();

  await expect(page).toHaveURL(/\/assistant\/7$/);
  await expect(page.locator("#assistant-input")).toHaveValue("Also check the ElevenLabs keys");
  expect(errors).toEqual([]);
});

test("New chat gives a blank chat, and coming back keeps it blank", async ({ page, run, errors }) => {
  await mockChat(page, run.now);
  await page.goto("/assistant/7");
  await expect(page.getByText("Delete key", { exact: false }).first()).toBeVisible();

  await page.getByRole("button", { name: "New chat", exact: true }).first().click();
  await expect(page).toHaveURL(/\/assistant$/);
  await expect(page.getByText("What can I help with?")).toBeVisible();

  await navLink(page, "Catalog").click();
  await expect(page).not.toHaveURL(/\/assistant/);
  await navLink(page, "Assistant").click();
  await expect(page).toHaveURL(/\/assistant$/);
  await expect(page.getByText("What can I help with?")).toBeVisible();
  expect(errors).toEqual([]);
});

test("an older message shows its inline keys as chips, not placeholder text", async ({ page, run, errors }) => {
  await mockChat(page, run.now);
  await page.goto("/assistant/7");
  await expect(page.getByText("Delete key", { exact: false }).first()).toBeVisible();

  await expect(page.getByText("3 keys secured")).toBeVisible();
  await expect(page.getByText("[key k1", { exact: false })).toHaveCount(0);
  await expect(page.getByText("sent to Claude as placeholders", { exact: false })).toHaveCount(0);
  const chips = page.getByRole("list", { name: "Secured keys" }).last();
  await expect(chips.getByText("acct-9")).toBeVisible();
  await expect(chips.getByText("…0aa4")).toBeVisible();
  expect(errors).toEqual([]);
});

test("with AI off, a new chat says how to turn it on", async ({ page, run, errors }) => {
  await mockChat(page, run.now);
  await page.route("**/api/admin/chat/threads", (r) =>
    r.fulfill({ json: { threads: [], ai: { enabled: false, model: "", reason: "Set ANTHROPIC_API_KEY and restart Acnologia." } } }),
  );
  await page.goto("/assistant");

  await expect(page.getByRole("heading", { name: "The Assistant needs an Anthropic API key" })).toBeVisible();
  await expect(page.getByText("Enter to send", { exact: false })).toHaveCount(0);
  await page.getByRole("button", { name: "Open Settings" }).click();
  await expect(page).toHaveURL(/\/settings$/);
  expect(errors).toEqual([]);
});
