import { toast as boardToast, type ToastOptions } from "@/components/base/toast/toast";

export type ToastKind = "success" | "error" | "info";

/** Extras a toast may carry: a description, actions such as Undo, a longer duration. */
export type ToastExtras = Omit<ToastOptions, "status">;

/**
 * Shim over BoardUI's toast so existing callers keep `toast.success(msg)`.
 * Errors stay up a little longer so there is time to read them.
 */
export const toast = {
  success: (m: string, o?: ToastExtras) => boardToast.success(m, { duration: 4000, ...o }),
  error: (m: string, o?: ToastExtras) => boardToast.error(m, { duration: 7000, ...o }),
  info: (m: string, o?: ToastExtras) => boardToast.info(m, { duration: 4000, ...o }),
};
