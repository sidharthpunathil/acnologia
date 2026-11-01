import { useEffect, useState } from "react";
import type { Provider, Service } from "../lib/types";
import { useLogoVersion, useProviders } from "../stores/providers";
import { cx } from "./ui";

export type LogoSize = "xs" | "sm" | "md" | "lg" | "xl";

/** One tile scale everywhere: 24px in lists and selects, 40px in cards, 56px in headers, 80px in the editor. */
const box: Record<LogoSize, string> = {
  xs: "h-5 w-5 rounded-[6px] text-[8px]",
  sm: "h-6 w-6 rounded-[7px] text-[9px]",
  md: "h-10 w-10 rounded-[11px] text-[14px]",
  lg: "h-14 w-14 rounded-[15px] text-[18px]",
  xl: "h-20 w-20 rounded-[20px] text-[24px]",
};

/** Even inner padding (about 14%) so square icons, wide wordmarks and tiny favicons sit the same. */
const pad: Record<LogoSize, string> = { xs: "p-[3px]", sm: "p-[3px]", md: "p-[6px]", lg: "p-2", xl: "p-3" };

function initials(name: string): string {
  const words = name.trim().split(/[\s._-]+/).filter(Boolean);
  if (!words.length) return "?";
  if (words.length === 1) return words[0].slice(0, 2);
  return (words[0][0] + words[1][0]).toUpperCase();
}

/** Picks ink or white letters for a colored tile, by relative luminance. */
function readableOn(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return "#ffffff";
  const n = parseInt(m[1], 16);
  const ch = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  const l = 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2];
  return l > 0.45 ? "#18181a" : "#ffffff";
}

function withVersion(url: string, v: number): string {
  if (!v) return url;
  return url + (url.includes("?") ? "&" : "?") + `v=${v}`;
}

/**
 * A logo tile. Shows the image when there is one, and a lettermark in the
 * provider color when there is none or the image fails to load.
 */
export function EntityLogo({
  src,
  name,
  color,
  size = "md",
  className,
}: {
  src: string | null | undefined;
  name: string;
  color?: string | null;
  size?: LogoSize;
  className?: string;
}) {
  const v = useLogoVersion((s) => s.v);
  const url = src ? withVersion(src, v) : null;
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [url]);

  if (url && !failed) {
    return (
      <div className={cx("flex shrink-0 items-center justify-center overflow-hidden bg-white shadow-[inset_0_0_0_1px_rgba(20,20,15,0.09)]", box[size], pad[size], className)}>
        <img
          src={url}
          alt=""
          loading="lazy"
          decoding="async"
          draggable={false}
          onError={() => setFailed(true)}
          className="h-full w-full select-none object-contain [image-rendering:auto]"
        />
      </div>
    );
  }

  const valid = color && /^#?[0-9a-f]{6}$/i.test(color.trim()) ? (color.startsWith("#") ? color : `#${color}`) : null;
  // Very light brand colors would wash out white letters, so darken those tiles to ink.
  const bg = valid && readableOn(valid) === "#ffffff" ? valid : valid ? "#18181a" : "#6b6b66";
  return (
    <div
      aria-hidden="true"
      className={cx("flex shrink-0 select-none items-center justify-center font-mono font-medium uppercase tracking-tight text-white", box[size], className)}
      style={{ backgroundColor: bg }}
    >
      {initials(name)}
    </div>
  );
}

export function ProviderLogo({ provider, size, className }: { provider: Pick<Provider, "name" | "color" | "logo_url">; size?: LogoSize; className?: string }) {
  return <EntityLogo src={provider.logo_url} name={provider.name} color={provider.color} size={size} className={className} />;
}

/** A service shows its own logo, then its provider's, then a lettermark in the provider color. */
export function ServiceLogo({ service, size, className }: { service: Pick<Service, "name" | "provider" | "logo_url">; size?: LogoSize; className?: string }) {
  const p = useProviders((s) => s.providers.find((x) => x.slug === service.provider));
  return <EntityLogo src={service.logo_url ?? p?.logo_url} name={service.name} color={p?.color} size={size} className={className} />;
}
