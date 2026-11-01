import { useEffect, useRef, useState } from "react";
import { cx } from "@/utils/cx";

const ENTITIES: Record<string, string> = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " ", "#39": "'" };

/** Turns text that may hold HTML into plain text: tags become spaces, common entities decode, whitespace collapses. */
export function plainText(s: string | null | undefined): string {
  if (!s) return "";
  return s
    .replace(/<(script|style)\b[^>]*>[\s\S]*?<\/\1\s*>/gi, " ")
    .replace(/<[^>]*>?/g, " ")
    .replace(/&(amp|lt|gt|quot|apos|nbsp|#39);/g, (_, e: string) => ENTITIES[e] ?? "")
    .replace(/\s+/g, " ")
    .trim();
}

/** BoardUI ease out for movement with presence. */
const EASE = "cubic-bezier(0.22, 1, 0.36, 1)";

function reduced() {
  return typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

/**
 * Text that crossfades when it changes. Old and new copies share one grid
 * cell, so the swap never moves anything around it. The exit (150ms) is
 * faster than the entrance (200ms). Colors come from the caller's token
 * classes. With reduced motion the fade stays but the blur is dropped.
 */
export function FadeText({ text, className }: { text: string; className?: string }) {
  const [layers, setLayers] = useState<{ id: number; text: string; leaving?: boolean }[]>([{ id: 0, text }]);
  const last = useRef(text);
  const next = useRef(1);

  useEffect(() => {
    if (text === last.current) return;
    last.current = text;
    const id = next.current++;
    setLayers((ls) => [...ls.filter((l) => !l.leaving).map((l) => ({ ...l, leaving: true })), { id, text }]);
    const t = window.setTimeout(() => setLayers((ls) => ls.filter((l) => !l.leaving)), 200);
    return () => window.clearTimeout(t);
  }, [text]);

  return (
    <span className="grid min-w-0">
      {layers.map((l) => (
        <span
          key={l.id}
          aria-hidden={l.leaving || undefined}
          className={cx("min-w-0 [grid-area:1/1]", className)}
          ref={(el) => {
            if (!el || l.id === 0 || el.dataset.animated === (l.leaving ? "out" : "in")) return;
            el.dataset.animated = l.leaving ? "out" : "in";
            const blur = reduced() ? [] : l.leaving ? ["blur(0px)", "blur(2px)"] : ["blur(2px)", "blur(0px)"];
            const frames = l.leaving
              ? [{ opacity: 1, filter: blur[0] ?? "none" }, { opacity: 0, filter: blur[1] ?? "none" }]
              : [{ opacity: 0, filter: blur[0] ?? "none" }, { opacity: 1, filter: blur[1] ?? "none" }];
            el.animate?.(frames, { duration: l.leaving ? 150 : 200, easing: EASE, fill: "forwards" });
          }}
        >
          {l.text}
        </span>
      ))}
    </span>
  );
}
