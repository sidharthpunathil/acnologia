import type { Page } from "@playwright/test";

// A canned Assistant conversation: a user paste, tool steps (one failed) and a
// confirmation waiting for an answer. Timestamps are relative to the frozen
// page clock, so relative labels are stable.

export async function mockChat(page: Page, now: number): Promise<void> {
  const t = (minutesAgo: number) => new Date(now - minutesAgo * 60_000).toISOString();
  const threads = [
    { id: 7, title: "Add SerpAPI keys from the team sheet", created_at: t(40), updated_at: t(2) },
    { id: 6, title: "Which ElevenLabs keys are out of quota?", created_at: t(300), updated_at: t(290) },
    { id: 5, title: "Set up a proxy app for the marketing site", created_at: t(2000), updated_at: t(1990) },
  ];
  const items = [
    // Keys the server moved to the vault: the text is only the prose, the keys come as label and hint.
    {
      id: "u0",
      kind: "user",
      ok: true,
      ts: t(8),
      text: "tavily api key add\nhttps://app.tavily.com/home",
      keys: 2,
      key_list: [
        { label: "sidharthpunathil", hint: "...Qus6" },
        { label: "", hint: "...8f2a" },
      ],
    },
    { id: "a0", kind: "assistant", ok: true, ts: t(8), text: "Added both keys to `tavily`. Both work." },
    { id: "u1", kind: "user", ok: true, ts: t(6), text: "Add these SerpAPI keys:\nacct-7, 3f9a...c21e\nacct-8, 77b2...0aa4\nacct-9, d01c...91fe" },
    { id: "a1", kind: "assistant", ok: true, ts: t(6), text: "I'll add the three keys to your `serpapi` service and check each one." },
    {
      id: "t1",
      kind: "tool",
      ok: true,
      ts: t(6),
      tool: "add_keys",
      summary: "Added 3 keys to SerpAPI (acct-7, acct-8, acct-9)",
      link: "/services/1",
      input: { service: "serpapi", keys: [{ label: "acct-7", secret: "...c21e" }, { label: "acct-8", secret: "...0aa4" }, { label: "acct-9", secret: "...91fe" }] },
      detail: JSON.stringify({ added: 3, skipped: 0 }),
    },
    { id: "t2", kind: "tool", ok: false, ts: t(5), tool: "check_keys", summary: "acct-9 was rejected: Invalid API key", detail: JSON.stringify({ checked: 3, valid: 2, invalid: ["acct-9"] }) },
    {
      id: "a2",
      kind: "assistant",
      ok: true,
      ts: t(5),
      text: "Two of the three keys work. acct-9 was rejected by SerpAPI, so it is out of rotation.\n- acct-7: 1,000 searches left\n- acct-8: 1,000 searches left\n- acct-9: invalid\n\nDo you want me to delete acct-9?",
    },
    { id: "u2", kind: "user", ok: true, ts: t(3), text: "Yes, delete it" },
    { id: "c1", kind: "confirm", ok: true, ts: t(3), text: "Delete key `acct-9` from SerpAPI? This cannot be undone.", pending: { id: "p1", status: "waiting" } },
  ];

  await page.route("**/api/admin/chat/threads", (r) =>
    r.fulfill({ json: { threads, ai: { enabled: true, model: "claude-opus-5-5", reason: "" } } }),
  );
  await page.route(/\/api\/admin\/chat\/threads\/\d+$/, (r) => {
    const id = Number(new URL(r.request().url()).pathname.split("/").pop());
    return r.fulfill({ json: { thread: threads.find((x) => x.id === id) ?? threads[0], items } });
  });
}
