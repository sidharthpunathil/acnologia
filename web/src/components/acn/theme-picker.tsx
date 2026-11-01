import { RiComputerLine, RiMoonLine, RiSunLine } from "@remixicon/react";
import { SegmentedControl, SegmentedControlItem } from "@/components/base/segmented-control/segmented-control";
import { useTheme, type ThemePref } from "@/stores/theme";
import { cx } from "@/utils/cx";

const ITEMS = [
  { id: "system" as ThemePref, label: "System", icon: RiComputerLine },
  { id: "light" as ThemePref, label: "Light", icon: RiSunLine },
  { id: "dark" as ThemePref, label: "Dark", icon: RiMoonLine },
];

/** System, Light or Dark. Saved in this browser. */
export function ThemePicker({ className }: { className?: string }) {
  const pref = useTheme((s) => s.pref);
  const setPref = useTheme((s) => s.setPref);
  return (
    <SegmentedControl
      aria-label="Theme"
      selectedKeys={[pref]}
      onSelectionChange={(keys) => {
        const next = [...keys][0] as ThemePref | undefined;
        if (next) setPref(next);
      }}
      className={cx("w-full", className)}
    >
      {ITEMS.map((it) => (
        <SegmentedControlItem key={it.id} id={it.id} className="flex-1 gap-1.5">
          <it.icon className="size-4 shrink-0" aria-hidden />
          {it.label}
        </SegmentedControlItem>
      ))}
    </SegmentedControl>
  );
}
