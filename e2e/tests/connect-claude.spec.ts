import { test, expect } from "../support/fixtures";

// The Connect Claude section of Settings, against a fixed control MCP tool
// list, so it runs the same on a server with or without /mcp/control.
const VIEW = {
  url: "https://acn.example.com/mcp/control",
  groups: [
    { id: "overview", label: "Overview", default: true },
    { id: "services", label: "Services", default: true },
    { id: "admin", label: "Admin", default: false },
    { id: "raw", label: "Raw API", default: false },
  ],
  tools: [
    { name: "overview", title: "Overview", description: "Counts of services, keys and apps, and what needs attention.", group: "overview", kind: "read", available: true },
    { name: "list_services", title: "List services", description: "Every service with its key counts.", group: "services", kind: "read", available: true },
    { name: "create_service", title: "Create service", description: "Add a service from a provider.", group: "services", kind: "write", available: true },
    { name: "delete_service", title: "Delete service", description: "Remove a service and its keys.", group: "services", kind: "destructive", available: true },
    { name: "list_users", title: "List users", description: "Everyone who can sign in.", group: "admin", kind: "read", available: true },
    { name: "api_request", title: "API request", description: "Call any /api/admin path.", group: "raw", kind: "write", available: false },
  ],
};

test("connect claude section", async ({ page }) => {
  await page.route("**/api/admin/mcp/control", (r) => r.fulfill({ json: VIEW }));
  await page.goto("/settings#connect-claude");
  const section = page.locator("section#connect-claude");
  await expect(section.getByRole("heading", { name: "Connect Claude" })).toBeVisible();

  // Defaults: the bare URL, and a placeholder where the token goes.
  await expect(section.getByText(VIEW.url, { exact: true })).toBeVisible();
  await expect(section.locator("pre").first()).toContainText("<your acnadm_ token>");

  // Options rewrite the URL and the snippets.
  await section.getByRole("row", { name: "Admin" }).click();
  await section.getByRole("switch", { name: "Read only" }).check({ force: true });
  await section.getByRole("switch", { name: "Ask before deleting" }).uncheck({ force: true });
  const url = `${VIEW.url}?groups=overview,services,admin&read_only=1&confirm=off`;
  await expect(section.getByText(url, { exact: true })).toBeVisible();
  await expect(section.locator("pre").first()).toContainText(`"${url}"`);

  await section.getByRole("tab", { name: "Claude Desktop" }).click();
  await expect(section.getByRole("tabpanel")).toContainText('"--groups",');
  await expect(section.getByRole("tabpanel")).toContainText('"--read-only"');

  // Read only leaves 3 of the 6 tools on: the reads in groups that are on.
  await expect(section.getByText("3 of 6 on")).toBeVisible();
});

test("connect claude on a server without the control MCP", async ({ page }) => {
  await page.route("**/api/admin/mcp/control", (r) =>
    r.fulfill({ status: 404, json: { error: { code: "not_found", message: "no such endpoint" } } }),
  );
  await page.goto("/settings#connect-claude");
  await expect(page.locator("section#connect-claude").getByText("Not on this server yet")).toBeVisible();
});
