import { expect, test } from "../support/fixtures";

// Several services of one account (each Omkar Cloud API, say) share its keys.
// The shared keys API is mocked, so the seeded server stays as it is.
// Behaviour only.

test("the catalog offers to link services to keys the account already has", async ({ page, errors }) => {
  let linked = false;
  await page.route(/\/api\/admin\/keys\/shared$/, (r) =>
    r.fulfill({
      json: linked
        ? { duplicates: [], suggestions: [] }
        : {
            duplicates: [],
            suggestions: [
              { service: "omkar-ahrefs", service_name: "Omkar Ahrefs", source: "omkar-cloud", source_name: "Omkar Cloud", source_keys: 1 },
              { service: "omkar-baidu", service_name: "Omkar Baidu", source: "omkar-cloud", source_name: "Omkar Cloud", source_keys: 1 },
            ],
          },
    }),
  );
  await page.route(/\/api\/admin\/keys\/shared\/link$/, (r) => {
    linked = true;
    return r.fulfill({ json: { linked: [{ service: "omkar-ahrefs" }, { service: "omkar-baidu" }] } });
  });

  await page.goto("/");
  await expect(page.getByText("Omkar Ahrefs, Omkar Baidu have no keys, but Omkar Cloud is the same account")).toBeVisible();
  await page.getByRole("button", { name: "Use those keys" }).click();
  await expect(page.getByText("2 services now use the keys you already have.")).toBeVisible();
  expect(errors).toEqual([]);
});

test("a key stored in two services says where its twin is", async ({ page, run, errors }) => {
  await page.route(/\/api\/admin\/services\/\d+\/keys$/, async (r) => {
    const res = await r.fetch();
    const json = await res.json();
    if (json.keys?.[0]) json.keys[0].twins = [{ key_id: 99, label: "acct-1", service_id: 2, service_slug: "omkar-cloud", service_name: "Omkar Cloud" }];
    await r.fulfill({ response: res, json });
  });
  await page.goto(`/services/${run.serviceIds.serpapi}`);
  await expect(page.getByText("Same key as in Omkar Cloud").filter({ visible: true }).first()).toBeVisible();
  expect(errors).toEqual([]);
});
