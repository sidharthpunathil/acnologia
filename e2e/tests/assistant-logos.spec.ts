import { expect, test } from "../support/fixtures";

// Logos in the Assistant: the logos a find_logo step found show as small
// pictures, and a logo question is a form of pictures plus "None of these".
// The chat API is mocked. Behaviour only.

// A 2x2 red PNG.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAEUlEQVR4nGP4z8DwH4QZYAwAR8oH+WdZbrcAAAAASUVORK5CYII=",
  "base64",
);

test("a logo question shows the candidates as pictures and sends the pick", async ({ page, run, errors }) => {
  const t = (m: number) => new Date(run.now - m * 60_000).toISOString();
  const thread = { id: 12, title: "Logos", created_at: t(5), updated_at: t(1) };
  const logos = [
    { id: "logo1", label: "linkedin.com · 180px" },
    { id: "logo2", label: "linkedin.com · 16px" },
  ];
  const items: Record<string, unknown>[] = [
    { id: "u1", kind: "user", ok: true, ts: t(3), text: "find the linkedin jobs logo" },
    { id: "t1", kind: "tool", tool: "find_logo", ok: true, ts: t(2), summary: "Found 2 logos for Omkar LinkedIn Jobs", logos },
    {
      id: "q1",
      kind: "ask",
      ok: true,
      ts: t(2),
      text: "Which logo?",
      ask: { id: "ask_1", kind: "logo", question: "Which logo should LinkedIn Jobs use?", choices: ["logo1", "logo2"], logos, status: "waiting" },
    },
  ];
  let answer: { ask_id: string; choice: string } | null = null;
  await page.route("**/api/admin/chat/threads", (r) =>
    r.fulfill({ json: { threads: [thread], ai: { enabled: true, model: "claude-opus-5-5", reason: "" } } }),
  );
  await page.route(/\/api\/admin\/chat\/threads\/12$/, (r) => r.fulfill({ json: { thread, items, turn: { active: false } } }));
  await page.route(/\/api\/admin\/chat\/threads\/12\/logos\/logo\d$/, (r) => r.fulfill({ body: PNG, contentType: "image/png" }));
  await page.route(/\/api\/admin\/chat\/threads\/12\/answer$/, async (r) => {
    answer = r.request().postDataJSON();
    const ask = items[2].ask as Record<string, unknown>;
    ask.status = "answered";
    ask.answer = "This one: linkedin.com · 180px";
    items.push({ id: "a2", kind: "assistant", ok: true, ts: t(0), text: "Set the LinkedIn logo." });
    await r.fulfill({ json: { thread, items: [items[2], items[3]], turn: { active: false } } });
  });

  await page.goto("/assistant/12");
  await expect(page.getByRole("list", { name: "Logos found" }).getByText("linkedin.com · 180px")).toBeVisible();
  const form = page.getByRole("group", { name: "Question" });
  await expect(form.getByText("Which logo should LinkedIn Jobs use?")).toBeVisible();
  await expect(form.getByRole("button", { name: "None of these" })).toBeVisible();
  await form.getByRole("button", { name: "Use this logo: linkedin.com · 180px" }).click();

  await expect(page.getByText("Set the LinkedIn logo.")).toBeVisible();
  expect(answer).toEqual({ ask_id: "ask_1", choice: "logo1" });
  expect(errors).toEqual([]);
});
