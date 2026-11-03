// The hero's ground: a field of characters on an 8 by 16 cell, drawn once
// (a composed still, no animation). It maps the request path beside it: a
// stream of characters runs into each stop of the panel, loose where the
// request arrives and packed where it reaches Acnologia. Colours come from
// the landing tokens, so it redraws when the theme changes. Decorative,
// hidden from assistive tech.
import { useEffect, useRef } from "react";

const CELL_W = 8;
const CELL_H = 16;
const RAMP = " .:-=+*#%@";

/** Small deterministic hash, so the field is the same on every load. */
function hash(x: number, y: number): number {
  let h = (x * 374761393 + y * 668265263) | 0;
  h = Math.imul(h ^ (h >>> 13), 1274126177);
  return ((h ^ (h >>> 16)) >>> 0) / 4294967295;
}

function smooth(t: number): number {
  return t * t * (3 - 2 * t);
}

/** Value noise on a coarse lattice. */
function noise(x: number, y: number): number {
  const xi = Math.floor(x);
  const yi = Math.floor(y);
  const xf = smooth(x - xi);
  const yf = smooth(y - yi);
  const a = hash(xi, yi);
  const b = hash(xi + 1, yi);
  const c = hash(xi, yi + 1);
  const d = hash(xi + 1, yi + 1);
  return a + (b - a) * xf + (c - a) * yf + (a - b - c + d) * xf * yf;
}

function draw(canvas: HTMLCanvasElement) {
  const rect = canvas.getBoundingClientRect();
  const dpr = Math.min(window.devicePixelRatio || 1, 2);
  const w = Math.max(1, Math.round(rect.width));
  const h = Math.max(1, Math.round(rect.height));
  canvas.width = w * dpr;
  canvas.height = h * dpr;
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);

  const css = getComputedStyle(canvas);
  const tints = ["--color-glyph-1", "--color-glyph-2", "--color-glyph-3", "--color-glyph-4", "--color-accent-deep"].map((v) =>
    css.getPropertyValue(v).trim(),
  );
  const mono = css.getPropertyValue("--font-outlier").trim();
  ctx.font = `11px ${mono}`;
  ctx.textBaseline = "top";

  // The field is the traffic on the request path: one stream per stop of
  // the panel, arriving from the left as loose noise and packing tight as
  // it reaches the panel; past the panel only the last stream (the request
  // with its key, on to the API) runs on, thinning out. Elsewhere, a faint
  // ground.
  const host = canvas.parentElement;
  const box = canvas.getBoundingClientRect();
  const panel = host?.querySelector(".lp-path")?.getBoundingClientRect();
  const stops = host ? [...host.querySelectorAll(".lp-stop")].map((el) => el.getBoundingClientRect()) : [];
  const left = panel ? panel.left - box.left : w * 0.55;
  const right = panel ? panel.right - box.left : w;
  const lanes = stops.map((r) => r.top - box.top + r.height / 2);
  const held = 1; // the middle stop, where the key is attached
  const sigma = 22;

  const cols = Math.ceil(w / CELL_W);
  const rows = Math.ceil(h / CELL_H);
  for (let r = 0; r < rows; r++) {
    const y = r * CELL_H + CELL_H / 2;
    for (let c = 0; c < cols; c++) {
      const x = c * CELL_W + CELL_W / 2;
      if (panel && x > left && x < right && y > panel.top - box.top && y < panel.bottom - box.top) continue;
      let stream = 0;
      let lane = -1;
      lanes.forEach((ly, i) => {
        const across = x <= left ? Math.min(1, x / Math.max(1, left)) : i === lanes.length - 1 ? Math.max(0, 1 - (x - right) / 260) : 0;
        const band = Math.exp(-((y - ly) ** 2) / (2 * sigma * sigma)) * across * across;
        if (band > stream) {
          stream = band;
          lane = i;
        }
      });
      const n = noise(c / 7, r / 3) * 0.6 + noise(c / 2.5, r / 1.4) * 0.4;
      const v = n * (0.1 + 0.95 * stream) - 0.12;
      if (v <= 0.03) continue;
      const level = Math.min(RAMP.length - 1, Math.floor(v * RAMP.length * 1.5));
      if (level < 1) continue;
      let tint = tints[Math.min(3, Math.floor((level / RAMP.length) * 4))];
      if (lane === held && level >= RAMP.length - 3) tint = tints[4];
      ctx.fillStyle = tint;
      ctx.fillText(RAMP[level], c * CELL_W, r * CELL_H + 2);
    }
  }
}

export function CipherField({ className }: { className?: string }) {
  const ref = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = ref.current;
    if (!canvas) return;
    let frame = 0;
    const redraw = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => draw(canvas));
    };
    redraw();
    const sized = new ResizeObserver(redraw);
    sized.observe(canvas);
    const themed = new MutationObserver(redraw);
    themed.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "class"] });
    void document.fonts?.ready.then(redraw);
    return () => {
      cancelAnimationFrame(frame);
      sized.disconnect();
      themed.disconnect();
    };
  }, []);

  return <canvas ref={ref} className={className} aria-hidden="true" />;
}
