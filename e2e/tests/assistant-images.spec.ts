import { expect, test } from "../support/fixtures";

// Images in the Assistant: picked (or pasted, or dropped) into the composer,
// shown as thumbnails, sent with the message and shown in your bubble. The
// chat API is mocked. Behaviour only.

// A 2x2 red PNG.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAEUlEQVR4nGP4z8DwH4QZYAwAR8oH+WdZbrcAAAAASUVORK5CYII=",
  "base64",
);

test("an image goes with the message and shows in the bubble", async ({ page, run, errors }) => {
  const t = (m: number) => new Date(run.now - m * 60_000).toISOString();
  const thread = { id: 11, title: "Images", created_at: t(5), updated_at: t(1) };
  const items: Record<string, unknown>[] = [{ id: "u1", kind: "user", ok: true, ts: t(2), text: "hi" }];
  let sent: { text: string; images?: { media_type: string; data: string }[] } | null = null;
  await page.route("**/api/admin/chat/threads", (r) =>
    r.fulfill({ json: { threads: [thread], ai: { enabled: true, model: "claude-opus-5-5", reason: "" } } }),
  );
  await page.route(/\/api\/admin\/chat\/threads\/11$/, (r) => r.fulfill({ json: { thread, items, turn: { active: false } } }));
  await page.route(/\/api\/admin\/chat\/threads\/11\/images\/img1$/, (r) => r.fulfill({ body: PNG, contentType: "image/png" }));
  await page.route(/\/api\/admin\/chat\/threads\/11\/messages$/, async (r) => {
    sent = r.request().postDataJSON();
    const item = { id: "u2", kind: "user", ok: true, ts: t(0), text: sent?.text ?? "", images: [{ id: "img1", media_type: "image/png" }] };
    items.push(item, { id: "a2", kind: "assistant", ok: true, ts: t(0), text: "A red square." });
    await r.fulfill({ json: { thread, items: items.slice(-2), turn: { active: false } } });
  });

  await page.goto("/assistant/11");
  await expect(page.getByText("hi", { exact: true })).toBeVisible();
  const picker = page.locator('input[type="file"][accept^="image/"]');
  await picker.setInputFiles({ name: "square.png", mimeType: "image/png", buffer: PNG });
  const strip = page.getByRole("list", { name: "Images to send" });
  await expect(strip.getByRole("img", { name: "square.png" })).toBeVisible();

  // Removing it empties the strip; adding again and sending with no text works.
  await strip.getByRole("button", { name: "Remove square.png" }).click();
  await expect(strip).toHaveCount(0);
  await picker.setInputFiles({ name: "square.png", mimeType: "image/png", buffer: PNG });
  await expect(page.getByRole("list", { name: "Images to send" })).toBeVisible();
  await page.locator("#assistant-input").press("Enter");

  await expect(page.getByText("A red square.")).toBeVisible();
  expect(sent).not.toBeNull();
  expect(sent!.images).toHaveLength(1);
  expect(sent!.images![0].media_type).toBe("image/png");
  expect(sent!.images![0].data.length).toBeGreaterThan(20);
  await expect(page.getByRole("list", { name: "Images" }).last().getByRole("img")).toBeVisible();
  await expect(page.getByRole("list", { name: "Images to send" })).toHaveCount(0);
  expect(errors).toEqual([]);
});
