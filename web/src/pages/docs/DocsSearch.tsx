import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useNavigate } from "react-router-dom";
import { RiCornerDownLeftLine, RiFileTextLine, RiHashtag, RiSearchLine } from "@remixicon/react";
import { Kbd } from "@/components/base/kbd/kbd";
import { Modal } from "@/components/base/modal/modal";
import { cx } from "@/utils/cx";
import { DOCS, groupOf, type DocEntry } from "./manifest";
import { loadOutlines, type DocOutline } from "./source";

interface Hit {
  key: string;
  doc: DocEntry;
  /** Set for a heading inside the doc; absent for the doc itself. */
  heading?: { id: string; text: string };
  /** A few words around the match when it was found in the body text. */
  snippet?: string;
  score: number;
}

const norm = (s: string) => s.toLowerCase().normalize("NFKD");

/** Every word of the query must appear; earlier and whole word matches rank higher. */
function score(text: string, words: string[]): number {
  const t = norm(text);
  let total = 0;
  for (const w of words) {
    const at = t.indexOf(w);
    if (at < 0) return 0;
    const wordStart = at === 0 || /[^a-z0-9]/.test(t[at - 1]);
    total += (wordStart ? 3 : 1) + (at === 0 ? 2 : 0);
  }
  return total;
}

function snippet(text: string, word: string): string {
  const at = norm(text).indexOf(word);
  const from = Math.max(0, at - 40);
  const cut = text.slice(from, at + word.length + 80).trim();
  return `${from > 0 ? "…" : ""}${cut}…`;
}

function search(query: string, outlines: DocOutline[]): Hit[] {
  const words = norm(query).split(/\s+/).filter(Boolean);
  if (!words.length) return DOCS.map((doc) => ({ key: doc.slug, doc, score: 0 }));
  const hits: Hit[] = [];
  for (const doc of DOCS) {
    const s = score(doc.title, words) * 3 || score(`${doc.title} ${doc.blurb}`, words);
    if (s) hits.push({ key: doc.slug, doc, score: s + 4 });
    const outline = outlines.find((o) => o.slug === doc.slug);
    const seen = new Set<string>();
    for (const h of outline?.headings ?? []) {
      if (h.depth < 2 || h.depth > 3) continue;
      const hs = score(h.text, words) * 2 || score(`${doc.title} ${h.text}`, words);
      if (hs) {
        seen.add(h.id);
        hits.push({ key: `${doc.slug}#${h.id}`, doc, heading: { id: h.id, text: h.text }, score: hs });
      }
    }
    // Then the body text, one hit per section, ranked below title and heading matches.
    for (const sec of outline?.sections ?? []) {
      const h = sec.heading;
      if (h && seen.has(h.id)) continue;
      if (!score(sec.text, words)) continue;
      const key = h ? `${doc.slug}#${h.id}` : `${doc.slug}#body`;
      const target = h && h.depth > 1 ? { id: h.id, text: h.text } : undefined;
      hits.push({ key, doc, heading: target, snippet: snippet(sec.text, words[0]), score: 0.5 });
    }
  }
  return hits.sort((a, b) => b.score - a.score).slice(0, 30);
}

/** The search dialog: client side over the docs' titles, blurbs and section headings. */
export function DocsSearch({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [outlines, setOutlines] = useState<DocOutline[]>([]);
  const [cursor, setCursor] = useState(0);
  const listId = useId();
  const list = useRef<HTMLUListElement>(null);

  useEffect(() => {
    if (!open) return;
    let live = true;
    loadOutlines().then((o) => live && setOutlines(o));
    return () => {
      live = false;
    };
  }, [open]);

  useEffect(() => {
    if (!open) setQuery("");
  }, [open]);

  const hits = useMemo(() => search(query, outlines), [query, outlines]);
  useEffect(() => setCursor(0), [query]);
  useEffect(() => {
    list.current?.querySelector<HTMLElement>(`[data-index="${cursor}"]`)?.scrollIntoView({ block: "nearest" });
  }, [cursor]);

  const go = (hit: Hit | undefined) => {
    if (!hit) return;
    onOpenChange(false);
    navigate(`/docs/${hit.doc.slug}${hit.heading ? `#${hit.heading.id}` : ""}`);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setCursor((c) => Math.min(c + 1, hits.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setCursor((c) => Math.max(c - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(hits[cursor]);
    }
  };

  const optionId = (i: number) => `${listId}-${i}`;

  return (
    <Modal
      isOpen={open}
      onOpenChange={onOpenChange}
      size="lg"
      aria-label="Search the docs"
      overlayClassName="items-start pt-[max(1rem,10vh)]"
      className="max-h-[min(36rem,80dvh)] gap-0 py-0"
    >
      <div className="flex items-center gap-3 border-b border-separator-border px-4">
        <RiSearchLine className="size-5 shrink-0 text-foreground-icon-tertiary" aria-hidden />
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Search the docs"
          aria-label="Search the docs"
          role="combobox"
          aria-expanded="true"
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={hits.length ? optionId(cursor) : undefined}
          enterKeyHint="go"
          spellCheck={false}
          className="h-14 min-w-0 flex-1 bg-transparent text-headline-regular text-text-primary outline-none placeholder:text-text-tertiary"
        />
        <Kbd className="hidden px-1.5 sm:inline-flex">Esc</Kbd>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-2">
        {!query.trim() ? <p className="px-2.5 pt-1.5 pb-1 text-caption-1-medium text-text-tertiary">All pages</p> : null}
        {hits.length ? (
          <ul ref={list} id={listId} role="listbox" aria-label="Results" className="flex flex-col gap-0.5">
            {hits.map((h, i) => {
              const on = i === cursor;
              const Icon = h.heading ? RiHashtag : RiFileTextLine;
              return (
                <li
                  key={h.key}
                  id={optionId(i)}
                  data-index={i}
                  role="option"
                  aria-selected={on}
                  onMouseMove={() => setCursor(i)}
                  onClick={() => go(h)}
                  className={cx(
                    "flex cursor-pointer items-center gap-3 rounded-2lg px-2.5 py-2",
                    on ? "bg-dropdown-item-hover-background" : "",
                  )}
                >
                  <Icon className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-body-medium text-text-primary">
                      {h.heading ? h.heading.text : h.doc.title}
                      {h.snippet ? <span className="text-text-tertiary"> in {h.doc.title}</span> : null}
                    </span>
                    <span className="truncate text-caption-1-regular text-text-tertiary">
                      {h.snippet ?? (h.heading ? `${groupOf(h.doc).title} / ${h.doc.title}` : h.doc.blurb)}
                    </span>
                  </span>
                  {on ? <RiCornerDownLeftLine className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden /> : null}
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="px-2.5 py-8 text-center text-body-regular text-text-secondary" role="status">
            Nothing matches “{query.trim()}”. Try a shorter word, such as lease or quota.
          </p>
        )}
      </div>
      <div className="hidden items-center gap-4 border-t border-separator-border px-4 py-2.5 text-caption-1-regular text-text-tertiary sm:flex">
        <span className="inline-flex items-center gap-1.5">
          <Kbd className="px-1.5">↑</Kbd>
          <Kbd className="px-1.5">↓</Kbd>
          to move
        </span>
        <span className="inline-flex items-center gap-1.5">
          <Kbd className="px-1.5">Enter</Kbd>
          to open
        </span>
      </div>
    </Modal>
  );
}
