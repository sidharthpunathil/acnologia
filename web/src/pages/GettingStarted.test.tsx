// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Checklist, Service } from "../lib/types";

const checklist = vi.fn<() => Promise<Checklist>>();
vi.mock("../lib/api", () => ({
  api: { checklist: () => checklist() },
}));
// The demo data button loads its own state; it is not what this test checks.
vi.mock("./DemoData", () => ({ DemoData: () => null }));

const { GettingStarted } = await import("./GettingStarted");

afterEach(cleanup);

function steps(done: Record<string, boolean>): Checklist {
  const ids = ["service", "app_token", "agent", "first_call"];
  const s = ids.map((id) => ({ id, title: id, done: !!done[id], next: {} }));
  const n = s.filter((x) => x.done).length;
  return { steps: s, done: n, total: 4, complete: n === 4 };
}

const svc = { id: 7, slug: "serp", name: "SerpAPI", key_counts: { total: 1 } } as unknown as Service;

describe("GettingStarted", () => {
  it("shows the steps the server's checklist says are done", async () => {
    checklist.mockResolvedValueOnce(steps({ service: true, app_token: true }));
    render(
      <MemoryRouter>
        <GettingStarted services={[svc]} onAddService={() => {}} />
      </MemoryRouter>,
    );
    expect(await screen.findByText(/2 of 4 done/)).toBeTruthy();
    // The next step to do is open: connecting an agent.
    expect(screen.getByText("CLI guide")).toBeTruthy();
    expect(checklist).toHaveBeenCalled();
  });

  it("hides once every step is done", async () => {
    checklist.mockResolvedValueOnce(steps({ service: true, app_token: true, agent: true, first_call: true }));
    const { container } = render(
      <MemoryRouter>
        <GettingStarted services={[svc]} onAddService={() => {}} />
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(checklist).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    expect(container.textContent).toBe("");
  });
});
