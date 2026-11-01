import { useEffect, useId, useState } from "react";
import { ChevronRight } from "lucide-react";
import type { Theme } from "../../stores/theme";
import { CodeBlock } from "../../components/CodeBlock";
import { CopyButton } from "../../components/CopyButton";

type MermaidApi = typeof import("mermaid").default;

// Mermaid is large, so it loads on the first page that has a diagram and is
// shared after that. Pages without diagrams never fetch it.
let mermaidPromise: Promise<MermaidApi> | null = null;
const loadMermaid = () => (mermaidPromise ??= import("mermaid").then((m) => m.default));

// mermaid.initialize is global, so renders run one at a time.
let queue: Promise<unknown> = Promise.resolve();

/**
 * Reads a theme color off the root element as a hex color, which is what
 * Mermaid's theme parser accepts. Tokens can be hex, oklch or color-mix(), so
 * the browser resolves the value and a 1px canvas flattens it to sRGB.
 */
function token(cssVar: string): string {
  const el = document.createElement("span");
  el.style.display = "none";
  el.style.color = `var(${cssVar})`;
  document.body.appendChild(el);
  const color = getComputedStyle(el).color;
  el.remove();
  const ctx = document.createElement("canvas").getContext("2d", { willReadFrequently: true });
  if (!ctx || !color) return "";
  ctx.fillStyle = color;
  ctx.fillRect(0, 0, 1, 1);
  const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
  return "#" + [r, g, b].map((n) => n.toString(16).padStart(2, "0")).join("");
}

function themeVariables(theme: Theme) {
  // BoardUI semantic tokens (styles/theme.css and styles/acnologia.css).
  const surface = token("--color-background-primary-default");
  const soft = token("--color-background-secondary-default");
  const well = token("--color-background-tertiary-default");
  const ink = token("--color-text-primary");
  const muted = token("--color-text-secondary");
  const line = token("--color-border-button-hover");
  const brandSoft = token("--color-brand-soft");
  return {
    darkMode: theme === "dark",
    background: surface,
    fontFamily: "'Inter Variable', Inter, ui-sans-serif, system-ui, sans-serif",
    fontSize: "14px",
    primaryColor: soft,
    primaryTextColor: ink,
    primaryBorderColor: line,
    secondaryColor: well,
    secondaryTextColor: ink,
    secondaryBorderColor: line,
    tertiaryColor: surface,
    tertiaryTextColor: ink,
    tertiaryBorderColor: line,
    lineColor: muted,
    textColor: ink,
    mainBkg: soft,
    nodeBorder: line,
    clusterBkg: surface,
    clusterBorder: line,
    titleColor: ink,
    edgeLabelBackground: surface,
    actorBkg: soft,
    actorBorder: line,
    actorTextColor: ink,
    actorLineColor: muted,
    signalColor: ink,
    signalTextColor: ink,
    labelBoxBkgColor: soft,
    labelBoxBorderColor: line,
    labelTextColor: ink,
    loopTextColor: ink,
    noteBkgColor: brandSoft,
    noteTextColor: ink,
    noteBorderColor: line,
    activationBkgColor: well,
    activationBorderColor: line,
    sequenceNumberColor: surface,
    stateBkg: soft,
    stateLabelColor: ink,
    compositeBackground: surface,
    altBackground: well,
    transitionColor: muted,
    transitionLabelColor: ink,
  };
}

async function renderDiagram(id: string, code: string, theme: Theme): Promise<string> {
  const mermaid = await loadMermaid();
  const run = queue.then(async () => {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: "base",
      themeVariables: themeVariables(theme),
      flowchart: { htmlLabels: true, curve: "basis" },
      sequence: { useMaxWidth: true },
    });
    const { svg } = await mermaid.render(id, code);
    return svg;
  });
  queue = run.catch(() => undefined);
  return run;
}

/** The width Mermaid drew at, from the max-width it puts on the SVG. */
function naturalWidth(svg: string): number {
  const m = /max-width:\s*([\d.]+)px/.exec(svg);
  return m ? Math.round(Number(m[1])) : 0;
}

/** A Mermaid diagram drawn in the page's theme, with its source one click away. */
export function Mermaid({ code, theme }: { code: string; theme: Theme }) {
  const rid = useId().replace(/[^a-zA-Z0-9]/g, "");
  const [svg, setSvg] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let live = true;
    renderDiagram(`mmd${rid}${theme}`, code, theme)
      .then((s) => {
        if (live) {
          setSvg(s);
          setFailed(false);
        }
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, [code, theme, rid]);

  if (failed) {
    return (
      <div className="my-6">
        <p className="mb-2 text-[13px] text-muted">This diagram could not be drawn. Here is its Mermaid source.</p>
        <CodeBlock code={code} label="mermaid" />
      </div>
    );
  }

  const kind = code.trim().split(/\s/)[0] ?? "diagram";
  return (
    <figure className="my-6 min-w-0 overflow-hidden rounded-xl border border-line bg-surface">
      <div className="flex items-center justify-between border-b border-line px-3 py-1.5">
        <span className="font-mono text-[11px] text-faint">diagram</span>
        <CopyButton text={code} label="Copy source" />
      </div>
      {svg ? (
        <div className="swap-in overflow-x-auto overscroll-x-contain px-4 py-5" tabIndex={0} aria-label="Diagram, scrolls sideways when wider than the screen">
          <div
            role="img"
            aria-label={`Diagram (${kind}). Its source follows.`}
            // On narrow screens a wide diagram keeps a readable size and scrolls inside its frame.
            style={{ minWidth: Math.min(naturalWidth(svg), 640) || undefined }}
            className="[&_svg]:mx-auto [&_svg]:h-auto"
            // Mermaid sanitizes its SVG output (securityLevel strict).
            dangerouslySetInnerHTML={{ __html: svg }}
          />
        </div>
      ) : (
        <div className="flex h-56 items-center justify-center" aria-busy="true">
          <div className="skeleton h-40 w-4/5 rounded-lg" />
          <span className="sr-only">Drawing diagram</span>
        </div>
      )}
      <details className="group border-t border-line">
        <summary className="tap flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-xs text-muted transition-colors hover:text-ink focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ink [&::-webkit-details-marker]:hidden">
          <ChevronRight aria-hidden className="h-3.5 w-3.5 transition-transform duration-150 ease-out group-open:rotate-90" />
          Diagram source
        </summary>
        <pre className="overflow-x-auto bg-code-bg px-4 py-3 font-mono text-[12.5px] leading-relaxed text-code-ink">
          <code>{code}</code>
        </pre>
      </details>
    </figure>
  );
}
