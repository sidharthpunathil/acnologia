/** Pure parsing for the agent notes card, kept free of React so it is easy to test. */

// A URL with a scheme, or a bare domain followed by a path (github.com/org/repo).
export const URL_RE = /\bhttps?:\/\/[^\s,;()<>]+|\b(?:[a-z0-9-]+\.)+[a-z]{2,}\/[^\s,;()<>]+/gi;
// Things a reader scans for: HTTP calls, headers, paths, files, snake_case codes, key=value.
export const CODE_RE =
  /\b(?:GET|POST|PUT|PATCH|DELETE|HEAD)\s+\/[^\s,;()]*|\bX-[A-Za-z0-9-]+(?::\s*\d+)?|(?<![\w/])\/[a-z0-9_<>{}-]+(?:\/[a-z0-9_<>{}.-]*)*|\b[\w-]+(?:\/[\w.-]+)+\.[a-z]{1,4}\b|\b[\w-]+\.(?:py|go|ts|tsx|js|md|txt|json|ya?ml)\b|\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+(?:=\S+?)?(?=[\s,;.)/]|$)/g;

export interface ParsedNotes {
  sentences: string[];
  sources: string[];
}

export function trimUrl(u: string): string {
  return u.replace(/[.,;:)]+$/, "");
}

/** Split the agent's summary into readable sentences and a deduped source list. */
export function parseNotes(summary: string): ParsedNotes {
  const text = summary.replace(/\s+/g, " ").trim();
  const at = text.search(/\bSources?:\s/i);
  const body = at >= 0 ? text.slice(0, at).trim() : text;
  const sourceText = at >= 0 ? text.slice(at) : "";
  const sources = [...new Set(Array.from(sourceText.match(URL_RE) ?? [], trimUrl))];
  const sentences = body
    // Agents often start a sentence with a lowercase product name, so any letter may follow;
    // common abbreviations do not end one.
    .split(/(?<!\b(?:e\.g|i\.e|vs|etc|approx|incl)\.)(?<=[.!?])\s+(?=[A-Za-z"(])/)
    .map((s) => s.trim())
    .filter(Boolean);
  return { sentences, sources };
}

