import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { RiLockLine, RiRefreshLine } from "@remixicon/react";
import { ApiError } from "@/lib/api";
import { useGate } from "@/stores/gate";
import { Button } from "@/components/base/buttons/button";
import { ButtonSpinner, Spinner } from "@/components/base/spinner/spinner";
import { Input } from "@/components/base/input/input";
import { Callout } from "@/components/acn/callout";
import { Mark } from "@/components/acn/brand";
import { AuthShell } from "./AuthShell";

/**
 * Renders children only once this browser is past the access gate. When the
 * server has no access password the gate is open and this adds one request.
 */
export function AccessGate({ children }: { children: ReactNode }) {
  const status = useGate((s) => s.status);
  const load = useGate((s) => s.load);

  useEffect(() => {
    void load();
  }, [load]);

  if (status === "open") return <>{children}</>;
  if (status === "locked") return <LockScreen />;
  if (status === "error") {
    return (
      <div role="alert" className="flex min-h-dvh flex-col items-center justify-center gap-4 bg-background-full px-6 text-center">
        <Mark size={48} />
        <h1 className="font-mono text-title-2-medium text-text-primary">Cannot reach Acnologia</h1>
        <p className="max-w-sm text-body-regular text-text-secondary">Make sure the server is running, then try again.</p>
        <Button variant="primary" leadingIcon={RiRefreshLine} onClick={() => void load()}>
          Try again
        </Button>
      </div>
    );
  }
  // Nothing behind the gate renders until the answer arrives, so no data
  // flashes on screen before the lock does.
  return (
    <div className="delayed-in flex min-h-dvh items-center justify-center bg-background-full" role="status" aria-live="polite">
      <Spinner size="lg" aria-label="Loading Acnologia" />
    </div>
  );
}

/** "0:42" under an hour, "3h 12m" beyond. */
function formatWait(sec: number): string {
  if (sec >= 3600) {
    const h = Math.floor(sec / 3600);
    const m = Math.ceil((sec % 3600) / 60);
    return `${h}h ${m}m`;
  }
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

/** The lock screen: one password, in the same shell as the sign-in page. */
export function LockScreen() {
  const unlock = useGate((s) => s.unlock);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [wrong, setWrong] = useState(false);
  const [other, setOther] = useState<string | null>(null);
  // Seconds left before the server takes another guess, after a 429.
  const [wait, setWait] = useState(0);
  const field = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (wait <= 0) return;
    const t = window.setTimeout(() => setWait((w) => w - 1), 1000);
    return () => window.clearTimeout(t);
  }, [wait]);

  // The wait ran out: back to the field.
  useEffect(() => {
    if (wait === 0) field.current?.focus();
  }, [wait]);

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!password || busy || wait > 0) return;
    setBusy(true);
    setWrong(false);
    setOther(null);
    try {
      await unlock(password);
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        // The limit, not a wrong answer: say so, and count down instead of
        // inviting a guess that cannot work.
        setWait(Math.max(1, err.retryAfterSec ?? 60));
      } else if (err instanceof ApiError && err.status === 401) {
        setWrong(true);
      } else {
        setOther(err instanceof Error ? err.message : "Something went wrong. Try again.");
      }
      setPassword("");
      setBusy(false);
    }
  };

  let alert: ReactNode = null;
  if (wait > 0) {
    alert = (
      <Callout tone="warning" title="Too many wrong passwords">
        <span aria-live="polite">Try again in {formatWait(wait)}.</span>
      </Callout>
    );
  } else if (wrong) {
    alert = <Callout tone="error">That password is not correct.</Callout>;
  } else if (other) {
    alert = <Callout tone="error">{other}</Callout>;
  }

  return (
    <AuthShell meta="Private Acnologia instance">
      <div className="flex w-full max-w-[400px] flex-col rounded-3xl border border-border-button-default bg-background-primary-default p-6 shadow-xs sm:p-8 dark:bg-background-secondary-default">
        <div className="flex flex-col gap-1.5">
          <h1 className="flex items-center gap-2 text-title-2-medium text-text-primary">
            <RiLockLine className="size-5 text-foreground-icon-secondary" aria-hidden />
            This instance is private
          </h1>
          <p className="text-body-regular text-text-secondary">
            Enter the access password to continue. You sign in to your account after this.
          </p>
        </div>
        <form onSubmit={(e) => void submit(e)} className="mt-6 flex flex-col gap-4">
          {alert}
          <Input
            ref={field}
            name="access-password"
            type="password"
            label="Access password"
            placeholder="Enter the access password"
            autoComplete="current-password"
            autoFocus
            revealable
            isRequired
            isInvalid={wrong}
            value={password}
            onChange={(v) => {
              setPassword(v);
              setWrong(false);
            }}
          />
          <Button
            type="submit"
            className="w-full"
            disabled={busy || wait > 0}
            aria-busy={busy || undefined}
            leadingIcon={busy ? ButtonSpinner : undefined}
          >
            Unlock
          </Button>
          <p className="text-caption-1-regular text-text-secondary">
            This browser stays unlocked for 30 days. Ask whoever runs this instance for the password.
          </p>
        </form>
      </div>
    </AuthShell>
  );
}
