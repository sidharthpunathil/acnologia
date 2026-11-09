import { expect, test } from "../support/fixtures";

// Capabilities: one job, several providers, one name for apps. The
// capability API is mocked, so the shared server (and the audit log the
// screenshot suites capture) stays as seeded. Behaviour only.

test("a capability lists its params, routes and URL, and saves edits", async ({ page, errors }) => {
  const cap = {
    id: 1,
    slug: "places",
    name: "Places",
    description: "Find businesses by keyword.",
    strategy: "round_robin",
    params: [{ name: "query", type: "string", required: true }],
    routes: [
      { service: "serpapi", path: "/search.json", query: { q: "{{query}}" }, output: { items: "local_results", fields: { name: "title" } } },
      { service: "brave", path: "/res/v1/web/search", query: { q: "{{query}}" } },
    ],
    created_at: "2026-11-01T00:00:00Z",
    updated_at: "2026-11-01T00:00:00Z",
  };
  let patched: Record<string, unknown> | null = null;
  await page.route(/\/api\/admin\/capabilities$/, (r) => r.fulfill({ json: { capabilities: [cap] } }));
  await page.route(/\/api\/admin\/capabilities\/1$/, async (r) => {
    patched = r.request().postDataJSON();
    Object.assign(cap, patched);
    await r.fulfill({ json: cap });
  });

  await page.goto("/capabilities");
  const card = page.getByRole("listitem").filter({ hasText: "/proxy/places" });
  await expect(card.getByText("Round robin")).toBeVisible();
  await expect(card.getByRole("list", { name: "Params" })).toContainText("query");
  const routes = card.getByRole("list", { name: "Routes, in priority order" });
  await expect(routes.getByRole("listitem")).toHaveCount(2);
  await expect(routes).toContainText("mapped");
  await expect(card.getByText("/proxy/places?query=...")).toBeVisible();

  await card.getByRole("button", { name: "Edit Places" }).click();
  const editor = page.getByLabel("Capability as JSON");
  const json = JSON.parse(await editor.inputValue());
  expect(json.slug).toBe("places");
  json.strategy = "cheapest";
  await editor.fill(JSON.stringify(json, null, 2));
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(card.getByText("Cheapest")).toBeVisible();
  // The slug is the public URL, so an edit never sends it.
  expect(patched).not.toHaveProperty("slug");
  expect(errors).toEqual([]);
});
