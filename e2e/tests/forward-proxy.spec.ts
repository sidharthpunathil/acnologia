import { expect, test } from "../support/fixtures";

// The forward proxy in the dashboard: the Integrate tab's third mode, and the
// Settings section with its Rotate CA confirm. The forward proxy API is
// mocked, so the run's real CA is never rotated. Behaviour only.

const SETUP = {
  proxy_url: "http://keys.example.com:8888",
  proxy_url_with_token: "http://app:%3Capp%20token%3E@keys.example.com:8888",
  ca_url: "http://keys.example.com/api/v1/proxy/ca.pem",
  ca_fingerprint: "AA:BB:CC:DD",
  passthrough: true,
  hosts: [
    { host: "serpapi.com", service: "serpapi", name: "SerpApi" },
    { host: "tiktok-scraper.omkar.cloud", service: "omkar-tiktok", name: "Omkar TikTok" },
  ],
  env: {},
  service_header: "X-Acnologia-Service",
  listen_addr: ":8888",
  main_port: true,
  configured_url: false,
};

test("the Integrate tab shows forward proxy snippets with the server's proxy URL", async ({ page, run, errors }) => {
  await page.route("**/api/admin/forward-proxy", (r) => r.fulfill({ json: SETUP }));
  await page.addInitScript(() => localStorage.setItem("acn.integrate.lang", "curl"));
  await page.goto(`/services/${run.serviceIds.serpapi}`);
  await page.getByRole("tab", { name: /^Integrate/ }).click();

  // The radio input is visually hidden; the whole card selects.
  await page.getByText("Forward proxy", { exact: true }).click();
  await expect(page.getByRole("radio", { name: /Forward proxy/ })).toBeChecked();
  const code = page.locator("pre").first();
  await expect(code).toContainText("curl -x 'http://app:acn_...@keys.example.com:8888' --cacert acnologia-ca.pem");
  await expect(code).toContainText("/api/v1/proxy/ca.pem");
  await expect(page.getByText("X-Acnologia-Service: serpapi")).toBeVisible();
  await expect(page.getByRole("link", { name: "Download CA certificate" })).toHaveAttribute("href", "/api/v1/proxy/ca.pem");

  // Back to Proxy mode: the /proxy snippet returns.
  await page.getByText("Proxy mode", { exact: true }).click();
  await expect(code).toContainText("/proxy/serpapi");
  expect(errors).toEqual([]);
});

test("Settings shows the forward proxy and rotates the CA after a confirm", async ({ page, errors }) => {
  let rotated = 0;
  await page.route("**/api/admin/forward-proxy", (r) => r.fulfill({ json: SETUP }));
  await page.route("**/api/admin/forward-proxy/ca/rotate", (r) => {
    rotated++;
    return r.fulfill({ json: { ca_fingerprint: "11:22:33:44" } });
  });
  await page.goto("/settings#forward-proxy");

  const section = page.getByRole("region", { name: "Forward proxy" });
  await expect(section.getByText("http://app:<app token>@keys.example.com:8888")).toBeVisible();
  await expect(section.getByText("Main port and :8888")).toBeVisible();
  await expect(section.getByText("tiktok-scraper.omkar.cloud")).toBeVisible();
  await expect(section.getByText("AA:BB:CC:DD")).toBeVisible();

  await section.getByRole("button", { name: "Rotate CA" }).click();
  const dialog = page.getByRole("dialog", { name: "Rotate CA" });
  await expect(dialog).toContainText("download the new one");
  expect(rotated).toBe(0);
  await dialog.getByRole("button", { name: "Rotate CA" }).click();

  await expect(section.getByText("11:22:33:44")).toBeVisible();
  expect(rotated).toBe(1);
  expect(errors).toEqual([]);
});
