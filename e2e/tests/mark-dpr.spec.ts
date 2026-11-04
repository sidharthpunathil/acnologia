import type { Locator } from "@playwright/test";
import { expect, test } from "../support/fixtures";

// The mark is drawn from small webp files picked by srcset (96, 144 and
// 256px) instead of one 512px PNG. At 1x and at 3x, each place shows the
// smallest file that is still sharp, and looks the same as before. Pixels of
// the mark only, at device scale, so a 3x baseline really is 3x.

const where = [
  { name: "top bar", path: "/", signedIn: true, size: 28 },
  // The mark on the request path diagram, the largest on the landing page.
  { name: "landing", path: "/landing", signedIn: false, size: 40 },
  { name: "sign in", path: "/login", signedIn: false, size: 72 },
] as const;

/** The file srcset should pick for a mark of `size` CSS pixels on a `dpr` screen. */
function expected(size: number, dpr: number): string {
  const need = size * dpr;
  const w = [96, 144, 256].find((x) => x >= need) ?? 256;
  return `/mark-${w}.webp`;
}

async function markIn(loc: Locator): Promise<Locator> {
  const img = loc.locator('img[srcset*="/mark-"]').first();
  await expect(img).toBeVisible();
  await expect.poll(() => img.evaluate((i: HTMLImageElement) => i.complete && i.naturalWidth > 0)).toBe(true);
  return img;
}

for (const dpr of [1, 3]) {
  test.describe(`at ${dpr}x`, () => {
    test.use({ deviceScaleFactor: dpr });

    for (const w of where) {
      test.describe(w.name, () => {
        if (!w.signedIn) test.use({ storageState: { cookies: [], origins: [] } });

        test(`mark ${w.name} ${dpr}x`, async ({ page, errors }, info) => {
          test.skip(info.project.name !== "desktop-light", "runs once, in desktop-light: the mark is the same in every theme");
          if (w.path === "/landing") await page.route("**/api/public/github*", (r) => r.fulfill({ json: { stars: 1 } }));
          await page.goto(w.path);
          const scope =
            w.path === "/" ? page.getByRole("link", { name: "Acnologia home" }) : w.path === "/landing" ? page.locator(".lp-stop--held") : page.locator("body");
          const img = await markIn(scope);
          expect(await img.evaluate((i: HTMLImageElement) => new URL(i.currentSrc).pathname)).toBe(expected(w.size, dpr));
          const box = await img.boundingBox();
          expect(box?.width).toBe(w.size);
          await expect(img).toHaveScreenshot(`mark-${w.name.replace(/ /g, "-")}-${dpr}x.png`, { scale: "device" });
          expect(errors).toEqual([]);
        });
      });
    }
  });
}
