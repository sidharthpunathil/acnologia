import { test } from "../support/fixtures";

// The docs pages embed animated demos (stageshow) in <figure> elements, which
// never hold still long enough to compare, so they are masked.

for (const [name, path] of [
  ["docs", "/docs"],
  ["docs-flows", "/docs/flows"],
  ["docs-api", "/docs/api"],
] as const) {
  test(`page ${name}`, async ({ page, snap }) => {
    await page.goto(path);
    await snap(name, { mask: [page.locator("figure")] });
  });
}
