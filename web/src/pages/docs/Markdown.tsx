import { createElement, memo, useMemo, type ReactNode } from "react";
import { Link } from "react-router-dom";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { Link2 } from "lucide-react";
import { CodeBlock } from "../../components/CodeBlock";
import type { Theme } from "../../stores/theme";
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

const HEADING_CLASS: Record<number, string> = {
  1: "text-balance text-[30px] font-semibold leading-tight tracking-tight text-ink-strong sm:text-[34px]",
  2: "mt-12 border-t border-line pt-8 text-[22px] font-semibold tracking-tight text-ink-strong",
  3: "mt-8 text-[17px] font-semibold text-ink-strong",
  4: "mt-6 text-[15px] font-semibold text-ink-strong",
  5: "mt-6 text-sm font-semibold text-ink-strong",
  6: "mt-6 text-sm font-semibold text-muted",
};

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
          { id, className: `group scroll-mt-28 ${HEADING_CLASS[depth]}` },
          children,
          id && depth > 1 ? (
            <a
              href={`#${id}`}
              className="ml-2 inline-flex align-middle text-faint opacity-0 transition-opacity duration-150 hover:text-ink focus-visible:opacity-100 group-hover:opacity-100"
              aria-label="Link to this section"
            >
              <Link2 className="h-4 w-4" aria-hidden />
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
      p: ({ children }) => <p className="mt-4 text-pretty leading-[1.75]">{children}</p>,
      a: ({ href = "", children }) => {
        const cls =
          "font-medium text-brand-ink underline decoration-brand/40 underline-offset-[3px] transition-colors hover:decoration-brand";
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
      ul: ({ children }) => <ul className="mt-4 list-disc space-y-1.5 pl-6 marker:text-faint">{children}</ul>,
      ol: ({ children }) => <ol className="mt-4 list-decimal space-y-1.5 pl-6 marker:text-faint">{children}</ol>,
      li: ({ children }) => <li className="pl-1 leading-[1.7] [&>p]:mt-2 [&>ul]:mt-2 [&>ol]:mt-2">{children}</li>,
      blockquote: ({ children }) => (
        <blockquote className="mt-5 border-l-2 border-brand/50 bg-soft py-1 pl-4 pr-3 text-muted [&>p]:mt-2">{children}</blockquote>
      ),
      hr: () => <hr className="my-10 border-line" />,
      strong: ({ children }) => <strong className="font-semibold text-ink-strong">{children}</strong>,
      img: ({ src, alt }) => (
        <img
          src={typeof src === "string" ? src : undefined}
          alt={alt ?? ""}
          loading="lazy"
          decoding="async"
          className="my-2 inline-block h-auto max-w-full rounded-lg border border-line bg-soft"
        />
      ),
      table: ({ children }) => (
        <div className="mt-5 max-w-full overflow-x-auto rounded-xl border border-line bg-surface" tabIndex={0} role="region" aria-label="Table">
          <table className="w-full border-collapse text-left text-[13.5px]">{children}</table>
        </div>
      ),
      thead: ({ children }) => <thead className="border-b border-line bg-soft text-[12px] text-faint">{children}</thead>,
      tbody: ({ children }) => <tbody className="divide-y divide-line">{children}</tbody>,
      th: ({ children, style }) => (
        <th style={style} className="whitespace-nowrap px-3.5 py-2.5 font-medium">
          {children}
        </th>
      ),
      td: ({ children, style }) => (
        <td style={style} className="min-w-[8rem] px-3.5 py-2.5 align-top leading-relaxed text-ink first:min-w-0">
          {children}
        </td>
      ),
      // Only inline code reaches here: `pre` below renders block code itself.
      code: ({ children }) => (
        <code className="rounded-[5px] border border-line bg-surface px-1.5 py-px font-mono text-[0.86em] text-ink-strong [overflow-wrap:anywhere]">{children}</code>
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
    <div className="min-w-0 text-[15px] text-ink [&>*:first-child]:mt-0">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components} skipHtml>
        {source}
      </ReactMarkdown>
    </div>
  );
});
