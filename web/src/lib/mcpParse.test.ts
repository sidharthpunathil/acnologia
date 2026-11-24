import { describe, expect, it } from "vitest";
import { parseMcpConnection } from "./mcpParse";

describe("parseMcpConnection", () => {
  it("returns null for empty input", () => {
    expect(parseMcpConnection("  \n ")).toBeNull();
  });

  it("reads a claude mcp add command with a bearer header", () => {
    const got = parseMcpConnection(
      `$ claude mcp add --transport http linear-server https://mcp.linear.app/mcp --header "Authorization: Bearer lin_api_123"`,
    );
    expect(got).toEqual({
      name: "Linear Server",
      slug: "linear-server",
      mcpUrl: "https://mcp.linear.app/mcp",
      authType: "bearer",
      authName: "Authorization",
      token: "lin_api_123",
      source: "Claude Code command",
    });
  });

  it("joins backslash continuations and reads a custom header", () => {
    const got = parseMcpConnection("claude mcp add -t http docs \\\n  https://docs.example.com/mcp/ \\\n  -H 'X-Api-Key: k_42'");
    expect(got?.mcpUrl).toBe("https://docs.example.com/mcp/");
    expect(got?.authType).toBe("header");
    expect(got?.authName).toBe("X-Api-Key");
    expect(got?.token).toBe("k_42");
  });

  it("treats a placeholder token as missing and picks up a token pasted below", () => {
    const got = parseMcpConnection(
      `claude mcp add notion https://mcp.notion.com/mcp --header "Authorization: Bearer <your-token>"\nToken: ntn_real_value`,
    );
    expect(got?.token).toBe("ntn_real_value");
  });

  it("reads an mcpServers JSON config", () => {
    const got = parseMcpConnection(
      JSON.stringify({
        mcpServers: {
          "github-tools": { url: "https://api.githubcopilot.com/mcp/", headers: { Authorization: "Bearer ghp_abc" } },
        },
      }),
    );
    expect(got).toMatchObject({
      name: "Github Tools",
      slug: "github-tools",
      mcpUrl: "https://api.githubcopilot.com/mcp/",
      authType: "bearer",
      token: "ghp_abc",
      source: "MCP JSON config",
    });
  });

  it("names a bare URL after its host, skipping an api or mcp label", () => {
    const got = parseMcpConnection("https://mcp.stripe.com/v1.");
    expect(got).toMatchObject({ name: "Stripe", slug: "stripe", mcpUrl: "https://mcp.stripe.com/v1", token: "" });
  });

  it("reads a labeled block with a token on the next line", () => {
    const got = parseMcpConnection("Server URL https://tools.acme.dev/mcp\nAPI key\nacme_live_9");
    expect(got).toMatchObject({ name: "Tools", mcpUrl: "https://tools.acme.dev/mcp", token: "acme_live_9", source: "pasted details" });
  });
});
