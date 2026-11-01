import { ThemePicker as AcnThemePicker } from "@/components/acn/theme-picker";

/** Legacy path over acn/theme-picker. `size` is accepted and ignored. */
export function ThemePicker({ className }: { className?: string; size?: "sm" | "md" }) {
  return <AcnThemePicker className={className} />;
}
