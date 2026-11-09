import { expect, test } from "../support/fixtures";

// Stop in the Assistant: while a turn runs the composer shows Stop, which
// ends the turn and leaves a "Stopped." line. The chat API is mocked.
// Behaviour only.

test("Stop ends a running turn", async ({ page, run, errors }) => {
  const t = (m: number) => new Date(run.now - m * 60_000).toISOString();
  const thread = { id: 21, title: "Catalog", created_at: t(5), updated_at: t(1) };
  const items: Record<string, unknown>[] = [{ id: "u1", kind: "user", ok: true, ts: t(1), text: "fix the catalog for all" }];
  let active = true;
  let stops = 0;
  await page.route("**/api/admin/chat/threads", (r) =>
    r.fulfill({ json: { threads: [thread], ai: { enabled: true, model: "claude-sonnet-5-5", reason: "" } } }),
  );
  await page.route(/\/api\/admin\/chat\/threads\/21$/, (r) =>
    r.fulfill({ json: { thread, items, turn: active ? { active: true, step: "Thinking", started_at: t(0) } : { active: false } } }),
  );
  await page.route(/\/api\/admin\/chat\/threads\/21\/stop$/, async (r) => {
    stops++;
    active = false;
    items.push({ id: "s1", kind: "stopped", ok: true, ts: t(0), text: "Stopped. The provider build it started was cancelled." });
    await r.fulfill({ json: { thread, items, turn: { active: false } } });
  });

  await page.goto("/assistant/21");
  const stop = page.getByRole("button", { name: "Stop", exact: true });
  await expect(stop).toBeVisible();
  await stop.click();

  await expect(page.getByText("Stopped. The provider build it started was cancelled.")).toBeVisible();
  await expect(stop).toBeHidden();
  expect(stops).toBe(1);
  expect(errors).toEqual([]);
});

test("Esc in an empty composer stops the turn", async ({ page, run }) => {
  const t = (m: number) => new Date(run.now - m * 60_000).toISOString();
  const thread = { id: 22, title: "Catalog", created_at: t(5), updated_at: t(1) };
  const items: Record<string, unknown>[] = [{ id: "u1", kind: "user", ok: true, ts: t(1), text: "verify everything" }];
  let active = true;
  await page.route("**/api/admin/chat/threads", (r) =>
    r.fulfill({ json: { threads: [thread], ai: { enabled: true, model: "claude-sonnet-5-5", reason: "" } } }),
  );
  await page.route(/\/api\/admin\/chat\/threads\/22$/, (r) =>
    r.fulfill({ json: { thread, items, turn: active ? { active: true, step: "Thinking", started_at: t(0) } : { active: false } } }),
  );
  await page.route(/\/api\/admin\/chat\/threads\/22\/stop$/, async (r) => {
    active = false;
    items.push({ id: "s1", kind: "stopped", ok: true, ts: t(0), text: "Stopped." });
    await r.fulfill({ json: { thread, items, turn: { active: false } } });
  });

  await page.goto("/assistant/22");
  await expect(page.getByRole("button", { name: "Stop", exact: true })).toBeVisible();
  await page.getByRole("textbox", { name: "Message" }).focus();
  await page.keyboard.press("Escape");
  await expect(page.getByText("Stopped.", { exact: true })).toBeVisible();
});
