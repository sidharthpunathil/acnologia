import { describe, expect, it } from "vitest";
import { countdown, formatUnlockTtl, plural, relTime, sentence, slugify } from "./format";

const now = Date.parse("2026-01-01T12:00:00Z");
const at = (sec: number) => new Date(now + sec * 1000).toISOString();

describe("relTime", () => {
  it("handles missing and invalid times", () => {
    expect(relTime(null, now)).toBe("never");
    expect(relTime("not a date", now)).toBe("-");
  });

  it.each([
    [5, "just now"],
    [-45, "45s ago"],
    [-180, "3m ago"],
    [7200, "in 2h"],
    [-3 * 86400, "3d ago"],
  ])("%i seconds reads %s", (sec, want) => {
    expect(relTime(at(sec), now)).toBe(want);
  });
});

describe("countdown", () => {
  it("is empty for a past or missing time", () => {
    expect(countdown(at(-5), now)).toBe("");
    expect(countdown(null, now)).toBe("");
  });

  it("shows minutes and seconds under an hour, hours and minutes above", () => {
    expect(countdown(at(245), now)).toBe("4:05");
    expect(countdown(at(3720), now)).toBe("1h 02m");
  });
});

describe("text helpers", () => {
  it("plural picks the word by count", () => {
    expect(plural(1, "key")).toBe("1 key");
    expect(plural(1200, "key")).toBe("1,200 keys");
    expect(plural(2, "proxy", "proxies")).toBe("2 proxies");
  });

  it("sentence capitalizes and adds a full stop only when missing", () => {
    expect(sentence("  key is invalid ")).toBe("Key is invalid.");
    expect(sentence("done!")).toBe("Done!");
    expect(sentence("   ")).toBe("");
  });

  it("slugify keeps lowercase letters and digits joined by single dashes", () => {
    expect(slugify("  My API: v2 (beta) ")).toBe("my-api-v2-beta");
  });

  it("formatUnlockTtl rounds to the largest whole unit", () => {
    expect(formatUnlockTtl(30 * 86400)).toBe("30 days");
    expect(formatUnlockTtl(86400)).toBe("1 day");
    expect(formatUnlockTtl(12 * 3600)).toBe("12 hours");
    expect(formatUnlockTtl(10)).toBe("1 minute");
  });
});
