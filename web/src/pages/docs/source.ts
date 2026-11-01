import { extractHeadings, type DocHeading } from "./headings";
import { DOCS, mdUrl } from "./manifest";

// Fetched Markdown, kept for the session so moving between docs is instant
// and search can reuse what the pages already loaded.
const cache = new Map<string, string>();

export const cachedDoc = (slug: string) => cache.get(slug);

export async function fetchDoc(slug: string, signal?: AbortSignal): Promise<string> {
  const hit = cache.get(slug);
  if (hit !== undefined) return hit;
  const res = await fetch(mdUrl(slug), { signal, headers: { Accept: "text/markdown" } });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const text = await res.text();
  cache.set(slug, text);
  return text;
}

export interface DocOutline {
  slug: string;
  headings: DocHeading[];
  /** Plain text under each heading (the intro has no heading), for full text search. */
  sections: Array<{ heading: DocHeading | null; text: string }>;
}

/** Markdown to searchable plain text: link targets, marks and table pipes dropped. */
function plainText(md: string): string {
  return md
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/[`*_>#|]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

export function outline(slug: string, text: string): DocOutline {
  const headings = extractHeadings(text);
  const lines = text.split("\n");
  const starts = [{ heading: null as DocHeading | null, line: 0 }, ...headings.map((h) => ({ heading: h, line: h.line }))];
  const sections = starts.map((st, i) => ({
    heading: st.heading,
    text: plainText(lines.slice(st.line, i + 1 < starts.length ? starts[i + 1].line - 1 : lines.length).join("\n")),
  }));
  return { slug, headings, sections };
}

let all: Promise<DocOutline[]> | null = null;

/** Every doc's headings, for search. Docs that fail to load are left out, and a later call tries again. */
export function loadOutlines(): Promise<DocOutline[]> {
  if (!all) {
    all = Promise.all(
      DOCS.map((d) =>
        fetchDoc(d.slug)
          .then((text) => outline(d.slug, text))
          .catch(() => ({ slug: d.slug, headings: [] as DocHeading[], sections: [], failed: true })),
      ),
    ).then((list) => {
      if (list.some((o) => "failed" in o)) all = null;
      return list.map(({ slug, headings, sections }) => ({ slug, headings, sections }));
    });
  }
  return all;
}
