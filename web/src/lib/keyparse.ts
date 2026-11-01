/**
 * Finds API keys in pasted text or dropped files.
 *
 * This mirrors server/internal/keyparse (Go). Both are checked against the
 * shared cases in server/internal/keyparse/testdata/cases.json, so change
 * them together. Rules, in order:
 *
 *  1. JSON: when the trimmed text is a JSON array, each string is a secret and
 *     each object takes its secret from key, secret, api_key, apiKey or token
 *     and its label from label, name or account (first non-empty wins). The
 *     secret must be 1 to 512 characters with no whitespace. Other entries go
 *     to ignored as their JSON text.
 *  2. Otherwise the text is split into trimmed lines. Empty lines and lines
 *     starting with # or // are skipped.
 *  3. CSV: when the first line, split on commas (or tabs when it has no comma),
 *     has a cell named label or name and one named key, secret or api_key, it
 *     is a header. Later lines are split the same way; a line whose secret
 *     cell is empty, too long or has whitespace is ignored whole.
 *  4. env: NAME=value or NAME: value (a space after the colon), with an
 *     optional "export " prefix, is label NAME and secret value when the
 *     cleaned value is key-like. Otherwise the line falls through to rule 5.
 *  5. Tokens: split on commas, semicolons, pipes and whitespace, then cleaned
 *     of surrounding quotes and brackets and trailing punctuation. A label-like
 *     token right before a key-like token labels it; other key-like tokens are
 *     unlabeled; every other token goes to ignored.
 *
 * Secrets are deduplicated in input order. A duplicate keeps the first
 * position and lends its label only when the first had none. Labels are cut
 * to 64 characters. At most MAX_KEYS keys come back; `overflow` counts the rest.
 */

export interface ParsedKey {
  label?: string;
  secret: string;
}

export interface ParseResult {
  keys: ParsedKey[];
  /** Tokens, lines or JSON items that held no key. At most MAX_IGNORED. */
  ignored: string[];
  /** Repeated secrets that were dropped. */
  duplicates: number;
  /** Distinct keys found past MAX_KEYS. */
  overflow: number;
}

export const MAX_KEYS = 5000;
export const MAX_IGNORED = 1000;
const MAX_SECRET = 512;
const MAX_LABEL = 64;

const SECRET_FIELDS = ["key", "secret", "api_key", "apiKey", "token"];
const LABEL_FIELDS = ["label", "name", "account"];
const KEY_CHARS = /^[A-Za-z0-9_\-.:+/=]+$/;
const ENV_LINE = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_.-]{0,63})\s*(?:=\s*|:\s+)(.+)$/;
const TOKEN_SPLIT = /[,;|\s]+/;
const LEADING_JUNK = /^["'`([{<]+/;
const TRAILING_JUNK = /["'`)\]}>.,;:!?]+$/;

export function isKeyLike(s: string): boolean {
  if (s.length < 16 || s.length > MAX_SECRET || s.includes("://")) return false;
  if (!KEY_CHARS.test(s)) return false;
  return /[0-9]/.test(s) || s.length >= 24;
}

function isLabelLike(s: string): boolean {
  const n = Array.from(s).length;
  if (n < 1 || n > MAX_LABEL || isKeyLike(s) || s.includes("://")) return false;
  return /\p{L}/u.test(s);
}

/** A secret given in an explicit field (JSON or a CSV column): present, not too long, one token. */
function plainSecret(s: string): boolean {
  return s !== "" && s.length <= MAX_SECRET && !/\s/.test(s);
}

export function cleanToken(s: string): string {
  for (;;) {
    const t = s.replace(LEADING_JUNK, "").replace(TRAILING_JUNK, "");
    if (t === s) return s;
    s = t;
  }
}

function cutLabel(s: string): string {
  const r = Array.from(s);
  return r.length <= MAX_LABEL ? s : r.slice(0, MAX_LABEL).join("").trim();
}

class Collector {
  keys: ParsedKey[] = [];
  ignored: string[] = [];
  duplicates = 0;
  overflow = 0;
  /** secret to index in keys, or -1 when it was over the cap. */
  private seen = new Map<string, number>();

  add(label: string, secret: string) {
    const l = cutLabel(label.trim());
    const i = this.seen.get(secret);
    if (i !== undefined) {
      this.duplicates++;
      if (i >= 0 && !this.keys[i].label && l) this.keys[i].label = l;
      return;
    }
    if (this.keys.length >= MAX_KEYS) {
      this.seen.set(secret, -1);
      this.overflow++;
      return;
    }
    this.seen.set(secret, this.keys.length);
    this.keys.push(l ? { label: l, secret } : { secret });
  }

  ignore(s: string) {
    if (!s || this.ignored.length >= MAX_IGNORED) return;
    this.ignored.push(s);
  }

  result(): ParseResult {
    return { keys: this.keys, ignored: this.ignored, duplicates: this.duplicates, overflow: this.overflow };
  }
}

function firstString(o: Record<string, unknown>, fields: string[]): string {
  for (const f of fields) {
    const v = o[f];
    if (typeof v === "string" && v.trim()) return v.trim();
  }
  return "";
}

function parseJSON(text: string, c: Collector): boolean {
  const t = text.trim();
  if (!t.startsWith("[")) return false;
  let data: unknown;
  try {
    data = JSON.parse(t);
  } catch {
    return false;
  }
  if (!Array.isArray(data)) return false;
  for (const item of data) {
    if (typeof item === "string") {
      const s = item.trim();
      if (plainSecret(s)) c.add("", s);
      else c.ignore(JSON.stringify(item));
      continue;
    }
    if (item && typeof item === "object" && !Array.isArray(item)) {
      const o = item as Record<string, unknown>;
      const secret = firstString(o, SECRET_FIELDS);
      if (plainSecret(secret)) {
        c.add(firstString(o, LABEL_FIELDS), secret);
        continue;
      }
    }
    c.ignore(JSON.stringify(item));
  }
  return true;
}

function splitCells(line: string, sep: string): string[] {
  return line.split(sep).map((x) => x.trim().replace(/^["'`]+|["'`]+$/g, "").trim());
}

function csvHeader(line: string): { sep: string; label: number; secret: number } | null {
  const sep = line.includes(",") ? "," : line.includes("\t") ? "\t" : "";
  if (!sep) return null;
  let label = -1;
  let name = -1;
  let secret = -1;
  splitCells(line, sep).forEach((cell, i) => {
    const c = cell.toLowerCase();
    if (c === "label" && label < 0) label = i;
    else if (c === "name" && name < 0) name = i;
    else if ((c === "key" || c === "secret" || c === "api_key") && secret < 0) secret = i;
  });
  if (label < 0) label = name;
  return label >= 0 && secret >= 0 ? { sep, label, secret } : null;
}

function parseLine(line: string, c: Collector) {
  const env = ENV_LINE.exec(line);
  if (env) {
    const v = cleanToken(env[2].trim());
    if (isKeyLike(v)) {
      c.add(env[1], v);
      return;
    }
  }
  let pending = "";
  for (const raw of line.split(TOKEN_SPLIT)) {
    if (!raw) continue;
    const tok = cleanToken(raw);
    if (!tok) {
      c.ignore(pending);
      pending = "";
    } else if (isKeyLike(tok)) {
      c.add(pending, tok);
      pending = "";
    } else if (isLabelLike(tok)) {
      c.ignore(pending);
      pending = tok;
    } else {
      c.ignore(pending);
      pending = "";
      c.ignore(tok);
    }
  }
  c.ignore(pending);
}

export function parseKeys(text: string): ParseResult {
  const c = new Collector();
  if (parseJSON(text, c)) return c.result();

  const lines = text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l && !l.startsWith("#") && !l.startsWith("//"));
  if (!lines.length) return c.result();

  const header = csvHeader(lines[0]);
  if (header) {
    for (const line of lines.slice(1)) {
      const cells = splitCells(line, header.sep);
      const secret = cells[header.secret] ?? "";
      if (!plainSecret(secret)) {
        c.ignore(line);
        continue;
      }
      c.add(cells[header.label] ?? "", secret);
    }
    return c.result();
  }

  for (const line of lines) parseLine(line, c);
  return c.result();
}

/**
 * Joins results parsed separately (pasted text, then each dropped file) with
 * the same dedupe, label and cap rules as one parse.
 */
export function mergeParsed(parts: ParseResult[]): ParseResult {
  if (parts.length === 1) return parts[0];
  const c = new Collector();
  for (const p of parts) {
    for (const k of p.keys) c.add(k.label ?? "", k.secret);
    for (const i of p.ignored) c.ignore(i);
    c.duplicates += p.duplicates;
    c.overflow += p.overflow;
  }
  return c.result();
}

/** "…c21e": enough to tell keys apart, never the whole secret. */
export function maskSecret(secret: string): string {
  return `…${secret.slice(-4)}`;
}

/** Shortens anything in an ignored item that might be a secret. */
export function maskLine(line: string, max = 80): string {
  const masked = line.replace(/[A-Za-z0-9_\-.:+/=]{12,}/g, (m) => (m.includes("://") ? m : maskSecret(m)));
  return masked.length > max ? `${masked.slice(0, max)}…` : masked;
}
