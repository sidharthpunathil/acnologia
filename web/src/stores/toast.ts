import { toast as boardToast } from "@/components/base/toast/toast";

export type ToastKind = "success" | "error" | "info";

/**
 * Shim over BoardUI's toast so existing callers keep `toast.success(msg)`.
 * Errors stay up a little longer so there is time to read them.
 */
export const toast = {
  success: (m: string) => boardToast.success(m, { duration: 4000 }),
  error: (m: string) => boardToast.error(m, { duration: 7000 }),
  info: (m: string) => boardToast.info(m, { duration: 4000 }),
};
