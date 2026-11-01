import {
  AudioLines,
  Bot,
  Brain,
  Cloud,
  Database,
  Globe,
  Image,
  KeyRound,
  Mail,
  MapPin,
  MessageSquare,
  Mic,
  Search,
  type LucideIcon,
} from "lucide-react";
import { cx } from "./ui";

const named: Record<string, { icon: LucideIcon; color: string }> = {
  serpapi: { icon: Search, color: "text-sky-600" },
  search: { icon: Search, color: "text-sky-600" },
  elevenlabs: { icon: AudioLines, color: "text-violet-600" },
  audio: { icon: AudioLines, color: "text-violet-600" },
  voice: { icon: Mic, color: "text-violet-600" },
  ai: { icon: Brain, color: "text-orange-600" },
  llm: { icon: Bot, color: "text-orange-600" },
  bot: { icon: Bot, color: "text-orange-600" },
  image: { icon: Image, color: "text-pink-600" },
  maps: { icon: MapPin, color: "text-emerald-600" },
  email: { icon: Mail, color: "text-amber-600" },
  mail: { icon: Mail, color: "text-amber-600" },
  chat: { icon: MessageSquare, color: "text-emerald-600" },
  data: { icon: Database, color: "text-slate-600" },
  database: { icon: Database, color: "text-slate-600" },
  cloud: { icon: Cloud, color: "text-sky-600" },
  web: { icon: Globe, color: "text-sky-600" },
  globe: { icon: Globe, color: "text-sky-600" },
  generic: { icon: KeyRound, color: "text-stone-500" },
};

/**
 * Icon tile for a service. The `icon` field is free form: a URL, an emoji,
 * or a short name ("serpapi", "search", "audio"). Falls back to the provider.
 */
export function ServiceIcon({ icon, provider, name, size = "md" }: { icon?: string; provider?: string; name?: string; size?: "md" | "lg" }) {
  const box = size === "lg" ? "h-12 w-12 rounded-[14px]" : "h-9 w-9 rounded-[10px]";
  const glyph = size === "lg" ? "h-5 w-5" : "h-4 w-4";
  const value = (icon ?? "").trim();

  let inner: JSX.Element;
  if (/^(https?:|data:|\/)/.test(value)) {
    inner = <img src={value} alt="" className={cx(glyph, "object-contain")} />;
  } else if (value && /[^\x00-\x7F]/.test(value) && value.length <= 4) {
    inner = <span className={size === "lg" ? "text-xl" : "text-base"}>{value}</span>;
  } else {
    const hit = named[value.toLowerCase()] ?? named[(provider ?? "").toLowerCase()];
    if (hit) {
      const I = hit.icon;
      inner = <I className={cx(glyph, hit.color)} strokeWidth={1.8} />;
    } else {
      inner = (
        <span className="font-mono text-[13px] font-medium uppercase text-muted">{(name ?? value ?? "?").slice(0, 2)}</span>
      );
    }
  }

  return (
    <div className={cx("flex shrink-0 items-center justify-center border border-line bg-[#fafaf7]", box)}>{inner}</div>
  );
}
