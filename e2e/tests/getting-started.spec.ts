import { expect, test } from "../support/fixtures";
import { signIn, startWorkbench, type Workbench } from "../support/workbench";

// The Getting started card on a fresh server's catalog. The main suite's
// server has done every step, so the card is hidden there. Loading the demo
// data changes the server, so each project runs on a workbench of its own.

test.use({ storageState: { cookies: [], origins: [] } });

test.describe("getting started", () => {
  let w: Workbench | undefined;

  test.beforeAll(async () => {
    w = await startWorkbench();
  });

  test.afterAll(() => {
    w?.close();
    w = undefined;
  });

  test("a fresh server shows the checklist, and Load demo data fills it in", async ({ page, snap }) => {
    await signIn(page, w!);
    await page.goto(`${w!.base}/`);
    const card = page.getByRole("region", { name: "Getting started" });
    await expect(card).toBeVisible();
    await expect(card.getByText("0 of 4 done", { exact: false })).toBeVisible();
    await expect(card.getByRole("button", { name: "Load demo data" })).toBeVisible();
    await snap("getting-started");

    await card.getByRole("button", { name: "Load demo data" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("heading", { name: "Demo data is ready" })).toBeVisible();
    await expect(page.locator("[data-entering]")).toHaveCount(0);
    await expect
      .poll(async () => JSON.stringify(await dialog.boundingBox()), { intervals: [150, 150, 150] })
      .toBe(JSON.stringify(await dialog.boundingBox()));
    // The app token is new each run, in the secret box and in the curl line.
    await snap("getting-started-demo-ready", {
      viewportOnly: true,
      mask: [dialog.getByText(/^acn_/), dialog.locator("pre")],
    });

    await dialog.getByRole("button", { name: "Done" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(card.getByText("Demo data is loaded")).toBeVisible();
    await expect(card.getByText("2 of 4 done", { exact: false })).toBeVisible();
    await snap("getting-started-demo-loaded");
  });
});
