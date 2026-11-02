import { useId, useMemo, useState, type ReactNode } from "react";
import { RiArrowDownSLine, RiExternalLinkLine } from "@remixicon/react";
import { AcnAiBuilder } from "@/components/foundations/icons/acn";
import { Section } from "@/components/acn";
import { cx } from "@/utils/cx";
import { safeHref } from "../../lib/url";
import { CODE_RE, URL_RE, parseNotes, trimUrl } from "./runNotesParse";

/** How many sentences show before the reader asks for the rest. */
const PREVIEW = 3;

function hrefFor(u: string): string | undefined {
  return safeHref(/^https?:\/\//i.test(u) ? u : `https://${u}`);
}

/** Render one sentence with links and code spans picked out. */
function Rich({ text }: { text: string }) {
  const out: ReactNode[] = [];
  const marks: { start: number; end: number; kind: "url" | "code" }[] = [];
  for (const m of text.matchAll(URL_RE)) marks.push({ start: m.index!, end: m.index! + trimUrl(m[0]).length, kind: "url" });
  for (const m of text.matchAll(CODE_RE)) {
    const start = m.index!;
    const end = start + m[0].length;
    if (!marks.some((k) => start < k.end && end > k.start)) marks.push({ start, end, kind: "code" });
  }
  marks.sort((a, b) => a.start - b.start);
  let pos = 0;
  marks.forEach((k, i) => {
    if (k.start > pos) out.push(text.slice(pos, k.start));
    const s = text.slice(k.start, k.end);
    const href = k.kind === "url" ? hrefFor(s) : undefined;
    out.push(
      href ? (
        <a
          key={i}
          href={href}
          target="_blank"
          rel="noreferrer"
          dir="ltr"
          className="rounded-sm text-text-primary underline decoration-separator-border underline-offset-2 outline-none hover:decoration-current focus-visible:ring-2 focus-visible:ring-border-focus-ring"
        >
          {s}
        </a>
      ) : (
        <code key={i} dir="ltr" className="rounded-md bg-background-tertiary-default px-1 py-px font-mono text-caption-1-regular text-text-primary">
          {s}
        </code>
      ),
    );
    pos = k.end;
  });
  if (pos < text.length) out.push(text.slice(pos));
  return <>{out}</>;
}

function SourceRow({ url }: { url: string }) {
  const href = hrefFor(url);
  let host = url;
  let path = "";
  try {
    const u = new URL(href ?? url);
    host = u.hostname.replace(/^www\./, "");
    path = (u.pathname + u.search).replace(/\/$/, "");
  } catch {
    /* keep the raw text */
  }
  const label = (
    <>
      <span className="shrink-0 text-text-primary">{host}</span>
      <span className="min-w-0 truncate text-text-tertiary">{path}</span>
    </>
  );
  return (
    <li>
      {href ? (
        <a
          href={href}
          target="_blank"
          rel="noreferrer"
          dir="ltr"
          title={url}
          className="group flex min-h-9 items-center gap-1 rounded-xl px-2.5 font-mono text-caption-1-regular outline-none transition-colors duration-150 ease-out hover:bg-background-secondary-hover focus-visible:ring-2 focus-visible:ring-border-focus-ring"
        >
          {label}
          <RiExternalLinkLine className="ms-auto size-3.5 shrink-0 text-foreground-icon-tertiary group-hover:text-foreground-icon-secondary" aria-hidden />
        </a>
      ) : (
        <span dir="ltr" className="flex min-h-9 items-center gap-1 px-2.5 font-mono text-caption-1-regular">
          {label}
        </span>
      )}
    </li>
  );
}

/**
 * What the agent learned while building the provider: one sentence per line,
 * identifiers set in code, long notes folded, and the sources as links.
 */
export function RunNotes({ summary, className }: { summary: string; className?: string }) {
  const { sentences, sources } = useMemo(() => parseNotes(summary), [summary]);
  const [open, setOpen] = useState(false);
  const listId = useId();
  if (sentences.length === 0 && sources.length === 0) return null;
  const folds = sentences.length > PREVIEW + 1;
  const shown = folds && !open ? sentences.slice(0, PREVIEW) : sentences;

  return (
    <Section
      className={className}
      title={
        <span className="flex items-center gap-2">
          <AcnAiBuilder className="size-4 text-foreground-icon-secondary" aria-hidden />
          Agent notes
        </span>
      }
      description="What the agent found while reading the docs. Check it before you publish."
    >
      <ul id={listId} className="flex flex-col gap-2.5">
        {shown.map((s, i) => (
          <li key={i} className="flex gap-2.5 text-body-2-regular leading-relaxed text-text-secondary">
            <span className="mt-[9px] size-1 shrink-0 rounded-full bg-foreground-icon-quaternary" aria-hidden />
            <span className="min-w-0 break-words">
              <Rich text={s} />
            </span>
          </li>
        ))}
      </ul>
      {folds && (
        <button
          type="button"
          aria-expanded={open}
          aria-controls={listId}
          onClick={() => setOpen(!open)}
          className="mt-3 flex min-h-9 items-center gap-1 rounded-md text-body-2-medium text-text-secondary outline-none transition-colors duration-150 ease-out hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
        >
          <RiArrowDownSLine className={cx("size-4 transition-transform duration-200 ease-out motion-reduce:transition-none", open && "rotate-180")} aria-hidden />
          {open ? "Show less" : `Show all ${sentences.length}`}
        </button>
      )}
      {sources.length > 0 && (
        <div className="mt-4 border-t border-separator-border pt-4">
          <h3 className="mb-1.5 text-caption-2-semibold uppercase text-text-tertiary">Sources</h3>
          <ul className="-mx-2.5 flex flex-col">
            {sources.map((u) => (
              <SourceRow key={u} url={u} />
            ))}
          </ul>
        </div>
      )}
    </Section>
  );
}
