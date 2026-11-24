// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { Key } from "../lib/types";
import { StatusPill, isBusy } from "./StatusPill";

afterEach(cleanup);

const now = Date.parse("2026-01-01T12:00:00Z");

function key(over: Partial<Key>): Key {
  return {
    effective_status: "active",
    in_flight: 0,
    max_concurrency: null,
    cooldown_until: null,
    quota_reset_at: null,
    ...over,
  } as Key;
}

describe("isBusy", () => {
  it("uses the key's own limit before the service's", () => {
    expect(isBusy({ in_flight: 2, max_concurrency: 2 }, 0)).toBe(true);
    expect(isBusy({ in_flight: 2, max_concurrency: 5 }, 1)).toBe(false);
    expect(isBusy({ in_flight: 1, max_concurrency: null }, 1)).toBe(true);
  });

  it("is never busy without a limit", () => {
    expect(isBusy({ in_flight: 50, max_concurrency: null }, 0)).toBe(false);
  });
});

describe("StatusPill", () => {
  it("reads available for an active key with room", () => {
    render(<StatusPill k={key({})} now={now} />);
    expect(screen.getByText("available")).toBeTruthy();
  });

  it("reads busy, not available, for an active key at its limit", () => {
    render(<StatusPill k={key({ in_flight: 3 })} now={now} serviceMax={3} />);
    expect(screen.getByText("busy")).toBeTruthy();
    expect(screen.queryByText("available")).toBeNull();
  });

  it("shows the cooldown left on a cooling key", () => {
    render(<StatusPill k={key({ effective_status: "cooling", cooldown_until: new Date(now + 65_000).toISOString() })} now={now} />);
    expect(screen.getByText("cooling")).toBeTruthy();
    expect(screen.getByText("1:05")).toBeTruthy();
  });

  it("shows when an exhausted key's quota resets", () => {
    render(<StatusPill k={key({ effective_status: "exhausted", quota_reset_at: new Date(now + 2 * 3600_000).toISOString() })} now={now} />);
    expect(screen.getByText("out of quota")).toBeTruthy();
    expect(screen.getByText("2h 00m")).toBeTruthy();
  });
});
