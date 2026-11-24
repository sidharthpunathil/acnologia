// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { safeAppPath, safeHref } from "./url";

describe("safeHref", () => {
  it.each([
    ["https://example.com/a?b=1", "https://example.com/a?b=1"],
    ["http://example.com", "http://example.com/"],
    ["javascript:alert(1)", undefined],
    ["data:text/html,hi", undefined],
    ["/relative", undefined],
    ["", undefined],
    [null, undefined],
  ])("%s", (input, want) => {
    expect(safeHref(input)).toBe(want);
  });
});

describe("safeAppPath", () => {
  it("keeps an in app path with its query and hash", () => {
    expect(safeAppPath("/services/3?tab=keys#top")).toBe("/services/3?tab=keys#top");
  });

  it.each(["//evil.example/x", "https://evil.example/x", "javascript:alert(1)", "services/3", "", null])("refuses %s", (input) => {
    expect(safeAppPath(input)).toBeUndefined();
  });
});
