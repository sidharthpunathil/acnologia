import { slugify } from "./format";
import type { AuthType } from "./types";

export interface ParsedMcpConnection {
  name: string;
  slug: string;
  mcpUrl: string;
  authType: AuthType;
  authName: string;
  token: string;
  /** Short human note of what format was recognized, e.g. "Claude Code command" or "mcpServers JSON". */
  source?: string;
}

const PLACEHOLDER_RE = /^(\.\.\.|<[^>]+>|your[_-]?.*|token|api[_-]?key|secret|xxx+|replace[_-]?me)$/i;

function cleanToken(raw: string): string {
  const t = raw.trim().replace(/^['"]|['"]$/g, "");
  if (!t || PLACEHOLDER_RE.test(t)) return "";
  return t;
}

function titleFromSlug(s: string): string {
  const parts = s
    .replace(/[_-]+/g, " ")
    .trim()
    .split(/\s+/)
    .filter(Boolean);
  if (!parts.length) return "";
  return parts.map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join(" ");
}

function nameFromUrl(rawUrl: string): string {
  try {
    const u = new URL(rawUrl);
    const host = u.hostname.replace(/^www\./i, "");
    const parts = host.split(".").filter(Boolean);
    const pick = parts.length >= 2 && ["api", "mcp", "app"].includes(parts[0].toLowerCase()) ? parts[1] : parts[0];
    return titleFromSlug(pick || host);
  } catch {
    return "";
  }
}

function parseHeaderLine(raw: string): { authType: AuthType; authName: string; token: string } | null {
  const idx = raw.indexOf(":");
  if (idx <= 0) return null;
  const headerName = raw.slice(0, idx).trim();
  const headerValue = raw.slice(idx + 1).trim();
  if (!headerName) return null;
  if (/^authorization$/i.test(headerName)) {
    const m = /^bearer\s+(.+)$/i.exec(headerValue);
    if (m) {
      return { authType: "bearer", authName: "Authorization", token: cleanToken(m[1]) };
    }
    return { authType: "header", authName: "Authorization", token: cleanToken(headerValue) };
  }
  return { authType: "header", authName: headerName, token: cleanToken(headerValue) };
}

/** Splits a shell command line into tokens, respecting single and double quotes. */
function splitShellArgs(cmd: string): string[] {
  const normalized = cmd.replace(/\\\r?\n/g, " ");
  const out: string[] = [];
  let cur = "";
  let quote: "'" | '"' | null = null;
  let escape = false;
  for (let i = 0; i < normalized.length; i++) {
    const ch = normalized[i];
    if (escape) {
      cur += ch;
      escape = false;
      continue;
    }
    if (ch === "\\" && quote !== "'") {
      escape = true;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = null;
      else cur += ch;
      continue;
    }
    if (ch === "'" || ch === '"') {
      quote = ch;
      continue;
    }
    if (/\s/.test(ch)) {
      if (cur) {
        out.push(cur);
        cur = "";
      }
      continue;
    }
    cur += ch;
  }
  if (cur) out.push(cur);
  return out;
}

function parseClaudeCommand(text: string): ParsedMcpConnection | null {
  // Match a claude mcp add line anywhere inside a pasted block.
  const lines = text.split(/\r?\n/);
  let cmdLine = "";
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (/^(?:\$\s*)?claude\s+mcp\s+add\b/i.test(line)) {
      cmdLine = line.replace(/^\$\s*/, "");
      // Join trailing backslash continuations.
      while (cmdLine.endsWith("\\") && i + 1 < lines.length) {
        cmdLine = cmdLine.slice(0, -1) + " " + lines[++i].trim();
      }
      break;
    }
  }
  if (!cmdLine) return null;

  const args = splitShellArgs(cmdLine);
  // Skip "claude", "mcp", "add"
  const positional: string[] = [];
  let authType: AuthType = "bearer";
  let authName = "Authorization";
  let token = "";

  for (let i = 3; i < args.length; i++) {
    const a = args[i];
    if (a === "--transport" || a === "-t" || a === "--scope" || a === "-s" || a === "--env" || a === "-e") {
      i++;
      continue;
    }
    if (a.startsWith("--transport=") || a.startsWith("--scope=") || a.startsWith("--env=")) {
      continue;
    }
    if (a === "--header" || a === "-H") {
      const hv = args[++i] ?? "";
      const parsed = parseHeaderLine(hv);
      if (parsed && (!token || parsed.token)) {
        authType = parsed.authType;
        authName = parsed.authName;
        if (parsed.token) token = parsed.token;
      }
      continue;
    }
    if (a.startsWith("--header=")) {
      const parsed = parseHeaderLine(a.slice("--header=".length));
      if (parsed && (!token || parsed.token)) {
        authType = parsed.authType;
        authName = parsed.authName;
        if (parsed.token) token = parsed.token;
      }
      continue;
    }
    if (a.startsWith("-")) continue;
    positional.push(a);
  }

  let rawName = "";
  let mcpUrl = "";
  for (const p of positional) {
    if (/^https?:\/\//i.test(p)) mcpUrl = p;
    else if (!rawName) rawName = p;
  }
  if (!mcpUrl) return null;

  const name = rawName ? titleFromSlug(rawName) : nameFromUrl(mcpUrl);
  const slug = slugify(rawName || name);
  return { name, slug, mcpUrl, authType, authName, token, source: "Claude Code command" };
}

function parseJsonConfig(text: string): ParsedMcpConnection | null {
  const start = text.indexOf("{");
  const end = text.lastIndexOf("}");
  if (start < 0 || end <= start) return null;
  let obj: Record<string, unknown>;
  try {
    obj = JSON.parse(text.slice(start, end + 1)) as Record<string, unknown>;
  } catch {
    return null;
  }
  if (!obj || typeof obj !== "object") return null;

  let rawName = "";
  let entry: Record<string, unknown> | null = null;
  const container = (obj.mcpServers ?? obj.servers) as Record<string, unknown> | undefined;
  if (container && typeof container === "object") {
    for (const [k, v] of Object.entries(container)) {
      if (v && typeof v === "object" && typeof (v as Record<string, unknown>).url === "string") {
        rawName = k;
        entry = v as Record<string, unknown>;
        break;
      }
    }
  } else if (typeof obj.url === "string") {
    entry = obj;
    if (typeof obj.name === "string") rawName = obj.name;
  }

  if (!entry || typeof entry.url !== "string" || !/^https?:\/\//i.test(entry.url.trim())) return null;
  const mcpUrl = entry.url.trim();
  let authType: AuthType = "bearer";
  let authName = "Authorization";
  let token = "";

  const headers = entry.headers;
  if (headers && typeof headers === "object") {
    for (const [hk, hv] of Object.entries(headers as Record<string, unknown>)) {
      if (typeof hv !== "string") continue;
      const parsed = parseHeaderLine(`${hk}: ${hv}`);
      if (parsed) {
        authType = parsed.authType;
        authName = parsed.authName;
        if (parsed.token) {
          token = parsed.token;
          break;
        }
      }
    }
  }

  const name = rawName ? titleFromSlug(rawName) : nameFromUrl(mcpUrl);
  const slug = slugify(rawName || name);
  return { name, slug, mcpUrl, authType, authName, token, source: "MCP JSON config" };
}

function parseLabeledText(text: string): ParsedMcpConnection | null {
  const urlMatch = text.match(/https?:\/\/[^\s"'<>]+/i);
  if (!urlMatch) return null;
  // Trim trailing punctuation that isn't part of a URL (keep trailing slash!).
  const mcpUrl = urlMatch[0].replace(/[),.;]+$/, "");

  let authType: AuthType = "bearer";
  let authName = "Authorization";
  let token = "";

  // Look for explicit "Authorization: Bearer ..." or header lines.
  const authLine = text.match(/([A-Za-z0-9-]+)\s*:\s*(Bearer\s+[^\r\n]+|[^\r\n]+)/i);
  if (authLine && !/^https?$/i.test(authLine[1])) {
    const parsed = parseHeaderLine(`${authLine[1]}: ${authLine[2]}`);
    if (parsed) {
      authType = parsed.authType;
      authName = parsed.authName;
      if (parsed.token) token = parsed.token;
    }
  }

  // Look for a labeled Token / API Key line (either "Token: xxx" or "Token\nxxx").
  if (!token) {
    const tokMatch = text.match(/(?:^|\n)\s*(?:token|api\s*key|bearer\s*token)\s*(?::|\n)\s*([^\s\r\n]+)/i);
    if (tokMatch) {
      token = cleanToken(tokMatch[1]);
    }
  }

  const name = nameFromUrl(mcpUrl);
  const slug = slugify(name);
  return {
    name,
    slug,
    mcpUrl,
    authType,
    authName,
    token,
    source: text.trim() === mcpUrl ? "MCP URL" : "pasted details",
  };
}

/**
 * Extracts upstream MCP connection details from a pasted Claude Code command,
 * Cursor/VS Code mcp.json snippet, labeled dashboard info block, or plain URL.
 * Exact trailing slashes on URLs are preserved.
 */
export function parseMcpConnection(raw: string): ParsedMcpConnection | null {
  const text = raw.trim();
  if (!text) return null;

  const fromCmd = parseClaudeCommand(text);
  if (fromCmd) {
    // If the command had a placeholder token, also check if a real token was pasted elsewhere in the block.
    if (!fromCmd.token) {
      const tokMatch = text.match(/(?:^|\n)\s*(?:token|api\s*key)\s*(?::|\n)\s*([^\s\r\n]+)/i);
      if (tokMatch) fromCmd.token = cleanToken(tokMatch[1]);
    }
    return fromCmd;
  }

  const fromJson = parseJsonConfig(text);
  if (fromJson) return fromJson;

  return parseLabeledText(text);
}
