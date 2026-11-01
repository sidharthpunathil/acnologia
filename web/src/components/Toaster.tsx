import { CheckCircle2, Info, X, XCircle } from "lucide-react";
import { useToasts } from "../stores/toast";
import { cx } from "./ui";

export function Toaster() {
  const { toasts, dismiss } = useToasts();
  return (
    <>
      {/* Polite region for success and info; assertive for errors. Both always mounted so readers pick up changes. */}
      <div className="sr-only" aria-live="polite" role="status">
        {toasts.filter((t) => t.kind !== "error").map((t) => (
          <p key={t.id}>{t.message}</p>
        ))}
      </div>
      <div className="sr-only" aria-live="assertive" role="alert">
        {toasts.filter((t) => t.kind === "error").map((t) => (
          <p key={t.id}>{t.message}</p>
        ))}
      </div>
      <div className="pointer-events-none fixed inset-x-0 bottom-4 z-[60] flex flex-col items-center gap-2 px-4 pb-[env(safe-area-inset-bottom)] sm:inset-x-auto sm:right-4 sm:items-end">
        {toasts.map((t) => (
          <div
            key={t.id}
            data-leaving={t.leaving ? "" : undefined}
            className={cx(
              "toast pointer-events-auto flex w-full max-w-sm items-start gap-2.5 rounded-xl bg-white py-3 pl-3.5 pr-2 text-sm text-ink shadow-pop",
            )}
          >
            {t.kind === "success" && <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-600" />}
            {t.kind === "error" && <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-red-600" />}
            {t.kind === "info" && <Info className="mt-0.5 h-4 w-4 shrink-0 text-muted" />}
            <div className="min-w-0 flex-1 break-words">{t.message}</div>
            <button
              type="button"
              aria-label="Dismiss notification"
              onClick={() => dismiss(t.id)}
              className="icon-btn -my-1 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-faint hover:bg-black/[0.05] hover:text-ink"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        ))}
      </div>
    </>
  );
}
