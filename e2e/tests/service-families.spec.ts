import { expect, test } from "../support/fixtures";

// Services one account's key serves fold into one catalog card. The services
// list is answered with a family made of two seeded services, so the shared
// server stays as seeded. Behaviour only.

test("a family of services is one card that opens and shares its key", async ({ page, run, errors }) => {
  const ids = [run.serviceIds.serpapi, run.serviceIds.brave];
  let linked = false;
  await page.route(/\/api\/admin\/services$/, async (r) => {
    if (r.request().method() !== "GET") return r.fallback();
    const res = await r.fetch();
    const json = await res.json();
    json.families = [{ key: "omkar.cloud|header|API-Key", name: "Omkar", main_id: ids[0], service_ids: ids, keys: 1, available: 1, unlinked: linked ? 0 : 1 }];
    await r.fulfill({ response: res, json });
  });
  await page.route(/\/api\/admin\/services\/families\/link$/, (r) => {
    linked = true;
    return r.fulfill({ json: { main: "serpapi", linked: ["brave"] } });
  });

  await page.goto("/");
  const card = page.getByRole("button", { name: /^Omkar/ });
  await expect(card).toBeVisible();
  await expect(card).toContainText("2 APIs");
  await expect(card).toContainText("1 of 1 key available");
  // Its members are inside it, not on their own.
  await expect(page.getByRole("region", { name: "Omkar services" })).toHaveCount(0);
  await card.click();
  const panel = page.getByRole("region", { name: "Omkar services" });
  await expect(panel.getByRole("link")).toHaveCount(2);

  await page.getByRole("button", { name: "Share keys" }).click();
  await expect(page.getByText("1 service now use the keys of Omkar.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Share keys" })).toHaveCount(0);
  expect(errors).toEqual([]);
});
