import { expect, test } from "../support/fixtures";

// A provider that serves many APIs on hosts of their own (Omkar Cloud's
// scrapers, say) and the Assistant queueing messages while a turn runs.
// Behaviour only, no screenshots.

test("a service lists its provider's APIs, grouped and searchable", async ({ page, run, errors }) => {
  // The provider list is answered with SerpAPI's spec carrying hosts, so
  // nothing on the shared server changes for the screenshot suites.
  const hosts = [
    { name: "google-maps-scraper", url: "https://google-maps-scraper.serpapi.com", title: "Google Maps Scraper", group: "Local & Maps", description: "Places and reviews." },
    { name: "tiktok-scraper", url: "https://tiktok-scraper.serpapi.com", title: "TikTok Scraper", group: "Social & Video", description: "Videos and users.", docs_url: "https://www.omkar.cloud/tools/tiktok-scraper" },
    { name: "amazon-scraper", url: "https://amazon-scraper.serpapi.com", title: "Amazon Scraper", group: "E-commerce", description: "Products and prices." },
  ];
  await page.route(/\/api\/admin\/providers(\?.*)?$/, async (route) => {
    const res = await route.fetch();
    const json = await res.json();
    for (const p of json.providers ?? []) if (p.slug === "serpapi") p.spec = { ...p.spec, hosts };
    await route.fulfill({ response: res, json });
  });

  await page.goto(`/services/${run.serviceIds.serpapi}`);
  const card = page.getByRole("region", { name: /APIs/ });
  await expect(card).toBeVisible();
  await expect(card.getByRole("heading", { name: "Social & Video" })).toBeVisible();
  await expect(card.getByText("~tiktok-scraper")).toBeVisible();
  await expect(card.getByRole("link", { name: "TikTok Scraper docs" })).toHaveAttribute("href", "https://www.omkar.cloud/tools/tiktok-scraper");

  await card.getByRole("searchbox", { name: "Search APIs" }).fill("amazon");
  await expect(card.getByText("~amazon-scraper")).toBeVisible();
  await expect(card.getByText("~tiktok-scraper")).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("messages sent while a turn runs wait in line and go out when it ends", async ({ page, run, errors }) => {
  const t = (m: number) => new Date(run.now - m * 60_000).toISOString();
  const thread = { id: 9, title: "Queue test", created_at: t(5), updated_at: t(1) };
  const items: Record<string, unknown>[] = [
    { id: "u1", kind: "user", ok: true, ts: t(2), text: "Check every key" },
  ];
  let active = true;
  const sent: string[] = [];
  await page.route("**/api/admin/chat/threads", (r) =>
    r.fulfill({ json: { threads: [thread], ai: { enabled: true, model: "claude-opus-5-5", reason: "" } } }),
  );
  await page.route(/\/api\/admin\/chat\/threads\/9$/, (r) =>
    r.fulfill({ json: { thread, items, turn: active ? { active: true, step: "Checking keys", started_at: t(1) } : { active: false } } }),
  );
  await page.route(/\/api\/admin\/chat\/threads\/9\/messages$/, async (r) => {
    const text = (r.request().postDataJSON() as { text: string }).text;
    sent.push(text);
    const item = { id: `u${sent.length + 1}`, kind: "user", ok: true, ts: t(0), text };
    items.push(item);
    await r.fulfill({ json: { thread, items: [item], turn: { active: false } } });
  });

  await page.goto("/assistant/9");
  const box = page.locator("#assistant-input");
  await expect(page.getByText("Check every key")).toBeVisible();

  await box.fill("Then rename the TikTok provider");
  await box.press("Enter");
  await box.fill("And fetch its logo");
  await box.press("Enter");

  const queue = page.getByRole("region", { name: "Queued messages" });
  await expect(queue.getByRole("listitem")).toHaveCount(2);
  await expect(box).toHaveValue("");
  expect(sent).toEqual([]);

  // Remove the second, then let the turn end: the first goes out on its own.
  await queue.getByRole("button", { name: "Remove queued message 2" }).click();
  await expect(queue.getByRole("listitem")).toHaveCount(1);
  active = false;
  await expect.poll(() => sent).toEqual(["Then rename the TikTok provider"]);
  await expect(queue).toHaveCount(0);
  expect(errors).toEqual([]);
});
