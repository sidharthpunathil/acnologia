import { beforeEach, describe, expect, it, vi } from "vitest";

const shown: { title: string; opts?: { description?: string; actions?: { label: string; onClick?: () => unknown }[] } }[] = [];
vi.mock("@/stores/toast", () => ({
  toast: {
    success: (title: string, opts?: (typeof shown)[number]["opts"]) => shown.push({ title, opts }),
    error: (title: string) => shown.push({ title }),
    info: (title: string) => shown.push({ title }),
  },
}));
const restore = vi.fn(async (_id: string) => ({ ok: true }));
vi.mock("@/lib/api", () => ({
  safetyApi: { restore: (id: string) => restore(id) },
  errorMessage: (e: unknown) => String(e),
}));

const { deletedToast, restoreIdOf } = await import("./trash");

describe("restoreIdOf", () => {
  it.each([
    [{ ok: true, restore_id: "trs_1" }, "trs_1"],
    [{ ok: true }, undefined],
    [{ restore_id: "" }, undefined],
    [null, undefined],
    ["trs_1", undefined],
  ])("%j", (res, want) => {
    expect(restoreIdOf(res)).toBe(want);
  });
});

describe("deletedToast", () => {
  beforeEach(() => {
    shown.length = 0;
    restore.mockClear();
  });

  it("offers Undo for an item in the trash, which restores it and reloads", async () => {
    const reload = vi.fn();
    deletedToast("billing", { ok: true, restore_id: "trs_9" }, reload);
    expect(shown[0].title).toBe("Deleted billing");
    expect(shown[0].opts?.description).toContain("Settings > Trash for 7 days");
    const undo = shown[0].opts?.actions?.find((a) => a.label === "Undo");
    expect(undo).toBeDefined();
    await undo?.onClick?.();
    expect(restore).toHaveBeenCalledWith("trs_9");
    expect(reload).toHaveBeenCalled();
    expect(shown[1].title).toBe("Restored billing");
  });

  it("says only deleted when the item skipped the trash", () => {
    deletedToast("peer key", { ok: true }, () => {});
    expect(shown).toEqual([{ title: "Deleted peer key", opts: undefined }]);
  });
});
