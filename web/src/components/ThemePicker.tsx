import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme, type ThemePref } from "../stores/theme";
import { Segmented } from "./ui";

const ITEMS = [
  { id: "system" as ThemePref, label: "System", icon: <Monitor className="h-3.5 w-3.5 shrink-0" aria-hidden="true" /> },
  { id: "light" as ThemePref, label: "Light", icon: <Sun className="h-3.5 w-3.5 shrink-0" aria-hidden="true" /> },
  { id: "dark" as ThemePref, label: "Dark", icon: <Moon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" /> },
];

/** System, Light or Dark. Saved in this browser. */
export function ThemePicker({ className, size }: { className?: string; size?: "sm" | "md" }) {
  const pref = useTheme((s) => s.pref);
  const setPref = useTheme((s) => s.setPref);
  return <Segmented items={ITEMS} value={pref} onChange={setPref} label="Theme" className={className} size={size} />;
}
