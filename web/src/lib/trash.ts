import { errorMessage, safetyApi } from "@/lib/api";
import type { DeleteResult } from "@/lib/types";
import { toast } from "@/stores/toast";

/** How long a deleted key, service or app stays in the trash. */
export const TRASH_DAYS = 7;

/** The confirm dialog line for a delete that goes to the trash. */
export const restoreNote = `You can restore it from Settings > Trash for ${TRASH_DAYS} days.`;

/** The restore id a delete answered with, if it went to the trash. */
export function restoreIdOf(res: unknown): string | undefined {
  if (!res || typeof res !== "object") return undefined;
  const id = (res as DeleteResult).restore_id;
  return typeof id === "string" && id !== "" ? id : undefined;
}

/**
 * The toast after a delete. When the item went to the trash it says so and
 * offers Undo, which restores it and then runs onRestored (to reload the
 * list it left).
 */
export function deletedToast(name: string, res: unknown, onRestored: () => unknown) {
  const id = restoreIdOf(res);
  if (!id) {
    toast.success(`Deleted ${name}`);
    return;
  }
  toast.success(`Deleted ${name}`, {
    description: `It stays in Settings > Trash for ${TRASH_DAYS} days.`,
    duration: 10000,
    actions: [
      {
        label: "Undo",
        onClick: async () => {
          try {
            await safetyApi.restore(id);
            await onRestored();
            toast.success(`Restored ${name}`);
          } catch (e) {
            toast.error(errorMessage(e));
          }
        },
      },
    ],
  });
}
