import type { ComponentType } from "react";

/** A Remix icon passed by reference (`RiKey2Line`, not `<RiKey2Line />`). */
export type IconComponent = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;
