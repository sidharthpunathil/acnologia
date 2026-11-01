import { Toaster as BoardToaster } from "@/components/base/toast/toast";

/**
 * The app's one toast stack: BoardUI's Toaster, lifted above the phone tab
 * bar. `toast` from stores/toast keeps working and draws here.
 */
export function Toaster() {
  return (
    <BoardToaster
      position="bottom-end"
      className="bottom-[calc(var(--tabbar-h)+0.75rem)] sm:bottom-[calc(var(--tabbar-h)+1.5rem)]"
    />
  );
}
