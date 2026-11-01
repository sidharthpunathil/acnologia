export interface DocHeading {
  depth: number;
  text: string;
  id: string;
  /** 1-based source line, matched against the rendered node's position. */
  line: number;
}

/** Plain text of a heading's inline Markdown: links keep their text, code and emphasis lose their marks. */
function plain(md: string): string {
  return md
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/[`*_]/g, "")
    .replace(/\s+#+\s*$/, "")
    .trim();
}

/** GitHub's anchor slug, so links such as flows#proxy-request keep working. */
export function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s_-]/gu, "")
    .trim()
    .replace(/\s/g, "-");
}

/** Every ATX heading outside fenced code, with unique ids in document order. */
export function extractHeadings(md: string): DocHeading[] {
  const out: DocHeading[] = [];
  const seen = new Map<string, number>();
  let fence = "";
  md.split("\n").forEach((raw, i) => {
    const line = raw.trimStart();
    const f = line.startsWith("```") ? "```" : line.startsWith("~~~") ? "~~~" : "";
    if (f) {
      if (!fence) fence = f;
      else if (f === fence) fence = "";
      return;
    }
    if (fence) return;
    const m = /^(#{1,6})\s+(.+)$/.exec(raw);
    if (!m) return;
    const text = plain(m[2]);
    const base = slugify(text);
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    out.push({ depth: m[1].length, text, id: n ? `${base}-${n}` : base, line: i + 1 });
  });
  return out;
}
