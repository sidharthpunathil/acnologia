import type { GuideDoc, Provider, Service, SpecExample } from "../../lib/types";
import { limitText, matchText, offersOf, priceText } from "../../lib/offers";

/*
 * A Markdown brief an LLM can act on with no other context: where to send
 * requests, how to authenticate, the endpoints the provider spec knows, and
 * what each Acnologia error means. It never contains a provider key: the
 * model calls Acnologia's proxy, which adds the key itself.
 */

export interface BriefInput {
  service: Pick<Service, "slug" | "name" | "description" | "base_url" | "max_attempts">;
  provider?: Pick<Provider, "name" | "website" | "docs_url" | "spec">;
  /** Acnologia origin, for example https://keys.example.com. */
  host: string;
  /** A real app token to embed, or null for a placeholder. */
  token: string | null;
  /** The provider's written guide, from the server. Generated guides add nothing the brief lacks. */
  guide?: Pick<GuideDoc, "summary" | "endpoints" | "notes" | "generated"> | null;
}

export const TOKEN_PLACEHOLDER = "ACNOLOGIA_TOKEN";

/** Inline code that survives backticks in the value. */
const ic = (v: string) => (v.includes("`") ? `\`\` ${v} \`\`` : `\`${v}\``);

/** A shell word in single quotes: nothing inside is expanded. */
const shq = (v: string) => `'${v.replace(/'/g, `'\\''`)}'`;

function exampleUrl(base: string, ex: SpecExample): string {
  const q = new URLSearchParams(Object.entries(ex.query ?? {})).toString();
  return `${base}${ex.path || "/"}${q ? `?${q}` : ""}`;
}

function curlFor(base: string, ex: SpecExample, auth: string): string {
  const method = (ex.method || "GET").toUpperCase();
  const lines = [`curl${method === "GET" ? "" : ` -X ${method}`} ${shq(exampleUrl(base, ex))}`, `  -H ${shq(`Authorization: Bearer ${auth}`)}`];
  if (ex.body !== undefined && ex.body !== null && method !== "GET" && method !== "HEAD") {
    lines.push(`  -H 'Content-Type: application/json'`);
    lines.push(`  -d ${shq(JSON.stringify(ex.body))}`);
  }
  return lines.join(" \\\n");
}

export function buildBrief({ service, provider, host, token, guide }: BriefInput): string {
  const base = `${host.replace(/\/$/, "")}/proxy/${service.slug}`;
  const auth = token ?? `$${TOKEN_PLACEHOLDER}`;
  const name = service.name;
  const spec = provider?.spec;
  const examples = (spec?.examples ?? []).slice(0, 8);
  const quota = spec?.quota;
  const out: string[] = [];

  out.push(`# Calling ${name} through Acnologia`, "");
  out.push(
    `You can call the ${name} API through Acnologia, a key pool. Send every ${name} request to the base URL below with the Acnologia token. Acnologia adds a working ${name} key, rotates keys on rate limits and retries up to ${service.max_attempts} times. You never see or need a ${name} key, so do not ask for one.`,
  );
  const written = guide && !guide.generated ? guide : null;
  if (written?.summary) out.push("", written.summary);
  else if (service.description) out.push("", `What ${name} does: ${service.description}`);
  out.push("");

  out.push("## Connection", "");
  out.push(`- Base URL: ${ic(base)}. It stands in for ${ic(service.base_url)}: keep the provider's paths, query and body exactly as its API documents them.`);
  if (token) {
    out.push(`- Token: ${ic(token)}`);
    out.push(`- Send it as ${ic(`Authorization: Bearer ${token}`)}. A tool that only takes a URL can add ${ic(`acn_token=${token}`)} to the query string instead.`);
  } else {
    out.push(`- Token: read it from the ${ic(TOKEN_PLACEHOLDER)} environment variable (an Acnologia app token that starts with ${ic("acn_")}).`);
    out.push(`- Send it as ${ic(`Authorization: Bearer $${TOKEN_PLACEHOLDER}`)}. A tool that only takes a URL can add ${ic(`acn_token=$${TOKEN_PLACEHOLDER}`)} to the query string instead.`);
  }
  out.push(`- Do not send ${name}'s own auth header or key parameter. Acnologia sets it.`);
  if (provider?.docs_url) out.push(`- ${name} API reference: ${provider.docs_url}`);
  out.push("");

  if (written?.endpoints?.length) {
    out.push("## Endpoints", "");
    for (const e of written.endpoints) {
      out.push(`### ${e.name}`, "", `${ic(`${(e.method || "GET").toUpperCase()} ${base}${e.path}`)}${e.description ? `: ${e.description}` : ""}`, "");
      for (const p of e.params ?? []) {
        const bits = [p.in, p.type, p.required ? "required" : "optional"].filter(Boolean).join(", ");
        out.push(`- ${ic(p.name)} (${bits})${p.description ? `: ${p.description}` : ""}`);
      }
      if (e.params?.length) out.push("");
      if (e.body !== undefined && e.body !== null) out.push("```json", JSON.stringify(e.body, null, 2), "```", "");
      if (e.response) out.push(`Response: ${e.response}`, "");
    }
  }

  if (examples.length) {
    out.push("## Endpoints with ready examples", "");
    for (const ex of examples) {
      out.push(`### ${ex.name || `${ex.method} ${ex.path}`}`, "");
      out.push("```sh", curlFor(base, ex, auth), "```", "");
    }
  } else {
    out.push("## Example", "");
    out.push("```sh", curlFor(base, { name: "", method: "GET", path: "/", safe: true }, auth), "```", "");
  }

  const offers = offersOf(spec);
  if (offers.length) {
    out.push("## Offers and limits", "");
    out.push(`${name} publishes these prices and limits. Limits are per key; Acnologia spreads requests over the pool's keys and holds a request back rather than exceed them, so stay well under them and prefer the free offers.`, "");
    for (const o of offers) {
      const bits = [priceText(o), ...(o.limits ?? []).map(limitText), o.concurrency ? `at most ${o.concurrency} at a time` : null].filter(Boolean);
      out.push(`- **${o.name}** (${ic(matchText(o))})${bits.length ? `: ${bits.join(", ")}` : ""}.${o.notes ? ` ${o.notes}` : ""}`);
    }
    const src = [...new Set(offers.map((o) => o.source_url).filter(Boolean))];
    const checked = offers.map((o) => o.checked_at ?? "").sort().pop() || undefined;
    if (src.length) out.push("", `Source: ${src.join(", ")}${checked ? ` (checked ${checked})` : ""}.`);
    out.push("");
  }

  if (quota?.unit || quota?.free_tier) {
    out.push("## Quota", "");
    if (quota.unit) out.push(`- Usage is counted in ${quota.unit}.`);
    if (quota.free_tier) out.push(`- Free tier: ${quota.free_tier}.`);
    out.push(`- Keep calls purposeful: every request spends quota from a shared pool.`, "");
  }

  if (written?.notes?.length) {
    out.push("## Notes", "");
    for (const n of written.notes) out.push(`- ${n}`);
    out.push("");
  }

  out.push("## Responses and errors", "");
  out.push(`- A ${name} response comes back unchanged, with headers ${ic("X-Acnologia-Key-Id")} (the key used) and ${ic("X-Acnologia-Attempts")}.`);
  out.push(`- ${ic("401 unauthenticated")}: the token is missing or wrong. Stop and ask the user for a valid token.`);
  out.push(`- ${ic("403 service_not_allowed")} or ${ic("proxy_not_allowed")}: this token may not call ${name} through the proxy. Ask the user to allow it on the Apps page.`);
  out.push(`- ${ic("503 no_key_available")}: every key is cooling down or out of quota. Wait the ${ic("retry_after_sec")} from the JSON body, then retry once.`);
  out.push(`- ${ic("503 all_keys_busy")}: every key is at its concurrency limit or at a limit listed above. Wait the ${ic("retry_after_sec")} from the JSON body, then retry.`);
  out.push(`- ${ic("503 service_unavailable")}: ${name} keeps failing upstream. Wait ${ic("retry_after_sec")} before retrying.`);
  out.push(`- Any other 4xx comes from ${name} itself, usually a bad parameter. Fix the request instead of retrying it as is.`);
  out.push("");

  return out.join("\n");
}
