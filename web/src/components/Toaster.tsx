import { CheckCircle2, Info, X, XCircle } from "lucide-react";
import { useToasts } from "../stores/toast";
import { cx } from "./ui";

export function Toaster() {
  const { toasts, dismiss } = useToasts();
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-4 z-[60] flex flex-col items-center gap-2 px-4 sm:inset-x-auto sm:right-4 sm:items-end">
      {toasts.map((t) => (
        <div
          key={t.id}
          role="status"
          className={cx(
            "toast-in pointer-events-auto flex w-full max-w-sm items-start gap-2.5 rounded-xl bg-white px-3.5 py-3 text-sm text-ink shadow-pop",
          )}
        >
          {t.kind === "success" && <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-600" />}
          {t.kind === "error" && <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-red-600" />}
          {t.kind === "info" && <Info className="mt-0.5 h-4 w-4 shrink-0 text-muted" />}
          <div className="min-w-0 flex-1 break-words">{t.message}</div>
          <button type="button" aria-label="Dismiss" onClick={() => dismiss(t.id)} className="text-faint hover:text-ink">
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}
