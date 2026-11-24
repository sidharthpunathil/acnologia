import { useEffect, useMemo, useState, type FormEvent } from "react";
import { RiPlayLine } from "@remixicon/react";
import { request, errorMessage } from "../../lib/api";
import type { Service } from "../../lib/types";
import { useApps } from "../../stores/apps";
import { useProviders } from "../../stores/providers";
import { docProviderSlug } from "../../lib/peerProviders";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { Textarea } from "@/components/base/textarea/textarea";
import { Select, SelectItem } from "@/components/base/select/select";
import { Checkbox } from "@/components/base/checkbox/checkbox";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { CodeBlock } from "@/components/acn";
import { cx } from "@/utils/cx";
import { CARD } from "./parts";

/*
 * Try it: send one request through the proxy, as an app or as the internal
 * Try it app, and see what came back. The server runs it on the same path an
 * app's request takes (POST /api/admin/services/{id}/try), so the app's
 * allowed services and read-only tokens apply, and the key is scrubbed from
 * the answer.
 */

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
const METHODS: Method[] = ["GET", "POST", "PUT", "PATCH", "DELETE"];
/** The internal Try it app, in the app select. */
const OWN = "own";

interface TryResult {
  status: number;
  latency_ms: number;
  headers: Record<string, string>;
  content_type: string;
  body: string;
  body_truncated: boolean;
  app: { id: number; name: string; read_only: boolean };
}

function statusColor(code: number): ChipColor {
  if (code >= 500) return "rose";
  if (code >= 400) return "orange";
  if (code >= 300) return "yellow";
  return "lime";
}

/** JSON bodies are shown indented; anything else as it came. */
function pretty(body: string, type: string): string {
  if (!/json/i.test(type) && !/^\s*[[{]/.test(body)) return body;
  try {
    return JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    return body;
  }
}

export function TryItPanel({ service }: { service: Service }) {
  const slug = docProviderSlug(service);
  const example = useProviders((st) => st.providers.find((p) => p.slug === slug)?.spec?.examples?.find((e) => e.safe));
  const { apps, loaded, fetch: fetchApps } = useApps();
  const [method, setMethod] = useState<Method>("GET");
  const [path, setPath] = useState("");
  const [body, setBody] = useState("");
  const [app, setApp] = useState<string>(OWN);
  const [readOnly, setReadOnly] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<TryResult | null>(null);

  useEffect(() => {
    if (!loaded) fetchApps().catch(() => undefined);
  }, [loaded, fetchApps]);

  // Start from the provider's first safe example, once it loads.
  useEffect(() => {
    if (!example || path) return;
    const q = example.query ? new URLSearchParams(example.query).toString() : "";
    setPath(example.path + (q ? `?${q}` : ""));
    if (METHODS.includes(example.method.toUpperCase() as Method)) setMethod(example.method.toUpperCase() as Method);
    if (example.body !== undefined && example.body !== null) setBody(JSON.stringify(example.body, null, 2));
    // Only the first load fills the form; later edits are the person's.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [example]);

  const usable = useMemo(() => apps.filter((a) => a.active && a.mode !== "lease"), [apps]);
  const hasBody = method !== "GET" && method !== "DELETE";

  const send = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await request<TryResult>("POST", `/api/admin/services/${service.id}/try`, {
        method,
        path: path.trim() || "/",
        body: hasBody ? body : "",
        app_id: app === OWN ? 0 : Number(app),
        read_only: readOnly,
      });
      setResult(res);
    } catch (err) {
      setResult(null);
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const headers = result ? Object.entries(result.headers).sort(([a], [b]) => a.localeCompare(b)) : [];

  return (
    <section aria-labelledby="try-it-title" className={cx(CARD, "mb-5 p-4 sm:p-5")}>
      <h2 id="try-it-title" className="text-headline-semibold text-text-primary">
        Try it
      </h2>
      <p className="mt-1 text-body-2-regular text-text-secondary">
        Send one request through the proxy and see what comes back. The key is added for you and removed from the answer.
      </p>

      <form onSubmit={send} className="mt-4 flex flex-col gap-4" noValidate>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <Select
            label="Method"
            selectedKey={method}
            onSelectionChange={(k) => k != null && setMethod(k as Method)}
            className="w-full sm:w-32"
          >
            {METHODS.map((m) => (
              <SelectItem key={m} id={m}>
                {m}
              </SelectItem>
            ))}
          </Select>
          <Input
            label="Path"
            value={path}
            onChange={setPath}
            placeholder="/v1/models?limit=5"
            autoComplete="off"
            spellCheck="false"
            className="min-w-0 flex-1"
            inputClassName="font-mono"
          />
        </div>
        {hasBody && (
          <Textarea label="Body (JSON)" rows={4} autoResize value={body} onChange={setBody} spellCheck="false" inputClassName="font-mono" />
        )}
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <Select
            label="Send as"
            selectedKey={app}
            onSelectionChange={(k) => k != null && setApp(String(k))}
            className="w-full sm:w-64"
          >
            {[
              <SelectItem key={OWN} id={OWN} textValue="Try it app">
                Try it app
              </SelectItem>,
              ...usable.map((a) => (
                <SelectItem key={a.id} id={String(a.id)} textValue={a.name}>
                  {a.name}
                </SelectItem>
              )),
            ]}
          </Select>
          <Checkbox isSelected={readOnly} onChange={setReadOnly} className="sm:pb-2.5">
            Read-only (GET only)
          </Checkbox>
          <Button
            type="submit"
            variant="primary"
            size="medium"
            className="sm:ms-auto"
            disabled={busy}
            leadingIcon={busy ? ButtonSpinner : RiPlayLine}
          >
            Send
          </Button>
        </div>
      </form>

      <div aria-live="polite">
        {error && <p className="mt-4 text-body-2-regular text-text-error-primary">{error}</p>}
        {result && (
          <div className="mt-5 flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <Chip variant="caption" color={statusColor(result.status)} className="font-mono">
                {result.status}
              </Chip>
              <span className="text-body-2-regular text-text-secondary">
                {result.latency_ms} ms, as {result.app.name}
                {result.app.read_only ? " (read-only)" : ""}
              </span>
            </div>
            <CodeBlock
              label={result.body_truncated ? "body, first 256 KB" : "body"}
              code={result.body ? pretty(result.body, result.content_type) : "(empty)"}
            />
            {headers.length > 0 && (
              <details className="group rounded-2xl border border-border-button-default">
                <summary className="cursor-pointer select-none rounded-2xl px-4 py-2.5 text-body-2-medium text-text-primary outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring">
                  Headers ({headers.length})
                </summary>
                <dl className="grid grid-cols-1 gap-x-4 gap-y-1 border-t border-separator-border px-4 py-3 sm:grid-cols-[minmax(0,14rem)_1fr]">
                  {headers.map(([k, v]) => (
                    <div key={k} className="contents">
                      <dt className="truncate font-mono text-caption-1-medium text-text-secondary">{k}</dt>
                      <dd className="mb-1 font-mono text-caption-1-regular text-text-primary wrap-anywhere sm:mb-0">{v}</dd>
                    </div>
                  ))}
                </dl>
              </details>
            )}
          </div>
        )}
      </div>
    </section>
  );
}
