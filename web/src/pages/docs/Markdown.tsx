import { createElement, memo, useMemo, type ReactNode } from "react";
import { Link } from "react-router-dom";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { RiLink } from "@remixicon/react";
import { CodeBlock } from "@/components/CodeBlock";
import type { Theme } from "@/stores/theme";
import { cx } from "@/utils/cx";
import type { DocHeading } from "./headings";
import { Mermaid } from "./Mermaid";

interface HastNode {
  type: string;
  value?: string;
  tagName?: string;
  properties?: Record<string, unknown>;
  children?: HastNode[];
  position?: { start: { line: number } };
}

function hastText(n: HastNode | undefined): string {
  if (!n) return "";
  if (n.type === "text") return n.value ?? "";
  return (n.children ?? []).map(hastText).join("");
}

/** Headings on BoardUI's composite type scale. */
const HEADING_CLASS: Record<number, string> = {
  1: "text-title-1-semibold text-balance text-text-primary sm:text-display-4-semibold",
  2: "mt-12 border-t border-separator-border pt-8 text-title-2-semibold text-text-primary",
  3: "mt-8 text-headline-semibold text-text-primary",
  4: "mt-6 text-body-semibold text-text-primary",
  5: "mt-6 text-body-2-semibold text-text-primary",
  6: "mt-6 text-body-2-semibold text-text-secondary",
};

const LINK = "rounded-sm text-text-primary underline decoration-chart-1 underline-offset-[3px] outline-none transition-[text-decoration-color] duration-150 hover:decoration-current focus-visible:ring-2 focus-visible:ring-border-focus-ring";

/**
 * A doc's Markdown with GitHub flavor (tables, task lists, autolinks). Raw
 * HTML is dropped. Headings carry GitHub style ids from `headings`.
 */
export const Markdown = memo(function Markdown({ source, headings, theme }: { source: string; headings: DocHeading[]; theme: Theme }) {
  const components = useMemo<Components>(() => {
    const idByLine = new Map(headings.map((h) => [h.line, h.id]));
    const heading = (depth: number) =>
      function H({ node, children }: { node?: unknown; children?: ReactNode }) {
        const line = (node as HastNode | undefined)?.position?.start.line ?? -1;
        const id = idByLine.get(line);
        return createElement(
          `h${depth}`,
          { id, className: cx("group scroll-mt-28", HEADING_CLASS[depth]) },
          children,
          id && depth > 1 ? (
            <a
              href={`#${id}`}
              className="ms-2 inline-flex rounded-sm align-middle text-foreground-icon-tertiary opacity-0 outline-none transition-opacity duration-150 group-hover:opacity-100 hover:text-foreground-icon-primary focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-border-focus-ring"
              aria-label="Link to this section"
            >
              <RiLink className="size-4" aria-hidden />
            </a>
          ) : null,
        );
      };
    return {
      h1: heading(1),
      h2: heading(2),
      h3: heading(3),
      h4: heading(4),
      h5: heading(5),
      h6: heading(6),
      p: ({ children }) => <p className="mt-4 text-pretty">{children}</p>,
      a: ({ href = "", children }) => {
        const cls = LINK;
        // Rendered doc pages stay in the app; raw files and the API go to the server.
        if (/^\/docs(\/[a-z-]+)?(#.*)?$/.test(href)) {
          return (
            <Link to={href} className={cls}>
              {children}
            </Link>
          );
        }
        if (/^https?:\/\//.test(href)) {
          return (
            <a href={href} className={cls} target="_blank" rel="noreferrer noopener">
              {children}
            </a>
          );
        }
        return (
          <a href={href} className={cls}>
            {children}
          </a>
        );
      },
      ul: ({ children }) => <ul className="mt-4 flex list-disc flex-col gap-1.5 ps-6 marker:text-text-tertiary">{children}</ul>,
      ol: ({ children }) => <ol className="mt-4 flex list-decimal flex-col gap-1.5 ps-6 marker:text-text-tertiary">{children}</ol>,
      li: ({ children }) => <li className="ps-1 [&>ol]:mt-2 [&>p]:mt-2 [&>ul]:mt-2">{children}</li>,
      // A note in the docs reads as BoardUI's info callout.
      blockquote: ({ children }) => (
        <blockquote className="mt-5 rounded-2xl bg-status-blue-background px-4 py-3 text-status-blue-text [&>p]:mt-2 [&>p:first-child]:mt-0 [&_a]:text-current [&_code]:bg-background-primary-default/60">
          {children}
        </blockquote>
      ),
      hr: () => <hr className="my-10 border-separator-border" />,
      strong: ({ children }) => <strong className="font-semibold text-text-primary">{children}</strong>,
      img: ({ src, alt }) => (
        <img
          src={typeof src === "string" ? src : undefined}
          alt={alt ?? ""}
          loading="lazy"
          decoding="async"
          className="my-2 inline-block h-auto max-w-full rounded-xl border border-border-button-default bg-background-secondary-default"
        />
      ),
      // GitHub tables on BoardUI's base table styles (.bui-table), in a card that scrolls sideways when wide.
      table: ({ children }) => (
        <div
          className="mt-5 max-w-full overflow-x-auto rounded-2xl border border-border-button-default bg-background-primary-default outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          tabIndex={0}
          role="region"
          aria-label="Table, scrolls sideways when wider than the screen"
        >
          {/* Cells get room to breathe; the first column holds labels (packages, settings, fields), so it keeps them on one line. */}
          <table className="bui-table bui-table-sm [&_tr>*:first-child]:ps-4! [&_tr>*:last-child]:pe-4! [&_th]:border-t-0! [&_td]:py-3! [&_th]:py-2.5!">{children}</table>
        </div>
      ),
      th: ({ children, style }) => (
        <th style={style} scope="col">
          {children}
        </th>
      ),
      td: ({ children, style }) => (
        <td
          style={style}
          className={cx(
            "min-w-[10rem] align-top! text-body-2-regular! leading-relaxed! sm:min-w-[12rem]",
            // The first column holds labels: each one stays whole, but a cell listing several can wrap between them.
            "first:w-[1%] first:min-w-0 first:pe-6! first:[&_code]:whitespace-nowrap first:[&_code]:[overflow-wrap:normal]",
          )}
        >
          {children}
        </td>
      ),
      // Only inline code reaches here: `pre` below renders block code itself.
      code: ({ children }) => (
        // Sized to the text around it (prose, table cells, headings), with slim padding so it reads as part of the sentence.
        <code className="rounded-[0.3em] bg-background-tertiary-default box-decoration-clone px-[0.3em] py-[0.1em] font-mono text-[0.875em] text-text-primary [overflow-wrap:anywhere]">{children}</code>
      ),
      pre: ({ node }) => {
        const codeEl = (node as HastNode | undefined)?.children?.find((c) => c.tagName === "code");
        const cls = codeEl?.properties?.className;
        const lang = (Array.isArray(cls) ? cls : [])
          .map(String)
          .find((c) => c.startsWith("language-"))
          ?.slice("language-".length);
        const code = hastText(codeEl).replace(/\n$/, "");
        if (lang === "mermaid") return <Mermaid code={code} theme={theme} />;
        return (
          <div className="mt-5">
            <CodeBlock code={code} label={lang ?? "text"} />
          </div>
        );
      },
    };
  }, [headings, theme]);

  return (
    <div className="min-w-0 text-body-regular text-text-primary sm:text-headline-regular [&>*:first-child]:mt-0 [&_li]:leading-relaxed [&_p]:leading-relaxed">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components} skipHtml>
        {source}
      </ReactMarkdown>
    </div>
  );
});
