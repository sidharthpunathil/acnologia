// "My access": a person pastes the token they got from a claim link and sees
// what it can use, how much is left and when it ends.
import { useEffect, useRef, useState, type FormEvent } from "react";
import { RiLogoutBoxRLine, RiRefreshLine } from "@remixicon/react";
import { useTitle } from "../../hooks/useTitle";
import { ApiError, errorMessage } from "../../lib/api";
import { dateTime, plural, relTime } from "../../lib/format";
import { fetchMe, forgetToken, meView, readSavedToken, saveToken, type MeView } from "../../lib/claim";
import type { ShareStatus } from "../../lib/sharing";
import { UsageMeters } from "../sharing/Limits";
import { Countdown } from "../sharing/Timers";
import { EntityLogo } from "../../components/EntityLogo";
import { Button } from "@/components/base/buttons/button";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Input } from "@/components/base/input/input";
import { Checkbox } from "@/components/base/checkbox/checkbox";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { Callout } from "@/components/acn";
import { PublicShell } from "./PublicRoutes";

const STATUS_COLOR: Record<ShareStatus, ChipColor> = { pending: "yellow", active: "lime", paused: "neutral", revoked: "rose", expired: "gray" };

const STATUS_NOTE: Partial<Record<ShareStatus, string>> = {
  paused: "The owner paused this access. Calls are refused until they resume it.",
  expired: "This access has ended. Ask the owner to extend it.",
  revoked: "The owner ended this access for good.",
};

function tokenError(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.status === 401 || e.status === 403) return "That token does not work. Check it was copied whole, or ask the owner whether your access was revoked.";
    if (e.status === 429) return `Too many tries. Wait ${e.retryAfterSec ? plural(e.retryAfterSec, "second") : "a minute"}, then try again.`;
    if (e.status === 404) return "This server cannot show access details yet.";
  }
  return errorMessage(e);
}

function Access({ v, onRefresh, onForget, busy }: { v: MeView; onRefresh: () => void; onForget: () => void; busy: boolean }) {
  const note = STATUS_NOTE[v.status];
  return (
    <>
      <section aria-labelledby="me-title" className="rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:p-6">
        <div className="flex flex-wrap items-center gap-2">
          <h1 id="me-title" className="min-w-0 wrap-anywhere text-title-2-medium text-text-primary">
            {v.name}
          </h1>
          <Chip variant="caption" color={STATUS_COLOR[v.status] ?? "neutral"}>
            {v.status}
          </Chip>
          <Chip variant="caption" color={v.mode === "reveal" ? "orange" : "neutral"}>
            {v.mode === "reveal" ? "keys shown" : "through the owner's server"}
          </Chip>
        </div>
        {v.owner && (
          <p className="mt-1 text-body-2-regular text-text-secondary">
            Shared by <bdi className="text-text-primary">{v.owner}</bdi>
          </p>
        )}
        <dl className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <dt className="text-caption-1-medium text-text-secondary">Ends</dt>
            <dd className="mt-0.5 text-body-2-regular text-text-primary">
              {v.endsAt ? (
                <span className="flex flex-col">
                  <span>{dateTime(v.endsAt)}</span>
                  {v.status === "active" && <Countdown iso={v.endsAt} subject={v.name} className="text-caption-1-regular text-text-secondary" />}
                </span>
              ) : (
                "No end date"
              )}
            </dd>
          </div>
          <div>
            <dt className="text-caption-1-medium text-text-secondary">Last used</dt>
            <dd className="mt-0.5 text-body-2-regular text-text-primary">{v.lastUsedAt ? relTime(v.lastUsedAt) : "Not yet"}</dd>
          </div>
        </dl>
        {note && (
          <Callout tone={v.status === "paused" ? "warning" : "neutral"} className="mt-4">
            {note}
          </Callout>
        )}
        {v.limitHit && (
          <Callout tone="warning" className="mt-4" title="Limit reached">
            Calls are refused until {dateTime(v.limitHit.reset_at)}.
          </Callout>
        )}
        <div className="mt-5 flex flex-wrap gap-2">
          <Button variant="secondary" size="small" leadingIcon={busy ? ButtonSpinner : RiRefreshLine} onClick={onRefresh} disabled={busy} aria-busy={busy || undefined}>
            Refresh
          </Button>
          <Button variant="ghost" size="small" leadingIcon={RiLogoutBoxRLine} onClick={onForget}>
            Forget token
          </Button>
        </div>
      </section>

      <section aria-labelledby="me-services" className="rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:p-6">
        <h2 id="me-services" className="text-headline-semibold text-text-primary">
          Services
        </h2>
        {v.services.length === 0 ? (
          <p className="mt-2 text-body-2-regular text-text-secondary">No services are listed for this token.</p>
        ) : (
          <ul className="mt-3 flex flex-col divide-y divide-separator-border">
            {v.services.map((s) => {
              const n = typeof s.available === "number" ? s.available : null;
              return (
                <li key={s.slug} className="flex min-w-0 items-center gap-3 py-2.5">
                  <EntityLogo src={s.logo_url ?? null} name={s.name || s.slug} size="sm" />
                  <div className="min-w-0 flex-1">
                    <div className="wrap-anywhere text-body-2-medium text-text-primary">{s.name || s.slug}</div>
                    <code dir="ltr" className="font-mono text-caption-1-regular text-text-secondary">
                      {s.slug}
                    </code>
                  </div>
                  {n !== null && (
                    <Chip variant="caption" color={n > 0 ? "lime" : "rose"} className="shrink-0">
                      {n > 0 ? `${plural(n, "key")} available` : "unavailable"}
                    </Chip>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section aria-labelledby="me-limits" className="rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:p-6">
        <h2 id="me-limits" className="text-headline-semibold text-text-primary">
          Limits and usage
        </h2>
        <p className="mt-1 mb-3 font-mono text-caption-1-regular text-text-secondary">
          {plural(v.usage.today_requests, "request")} today · {plural(v.usage.month_requests, "request")} this month
        </p>
        <UsageMeters limits={v.limits} usage={v.usage} />
      </section>
    </>
  );
}

export function MePage() {
  useTitle("My access");
  const [token, setToken] = useState("");
  const [remember, setRemember] = useState(false);
  const [view, setView] = useState<MeView | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const field = useRef<HTMLInputElement>(null);

  const look = async (t: string, keep: boolean) => {
    setBusy(true);
    setErr(null);
    try {
      const v = meView(await fetchMe(t));
      setView(v);
      if (keep) saveToken(t);
      else forgetToken();
    } catch (e) {
      setView(null);
      setErr(tokenError(e));
    } finally {
      setBusy(false);
    }
  };

  // A token remembered on this device opens straight to the details.
  useEffect(() => {
    const saved = readSavedToken();
    if (saved) {
      setToken(saved);
      setRemember(true);
      void look(saved, true);
    }
  }, []);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const t = token.trim();
    if (!t) {
      setErr("Paste the token you got from your link.");
      field.current?.focus();
      return;
    }
    void look(t, remember);
  };

  const forget = () => {
    forgetToken();
    setToken("");
    setRemember(false);
    setView(null);
    setErr(null);
    window.setTimeout(() => field.current?.focus(), 0);
  };

  return (
    <PublicShell>
      {view ? (
        <Access v={view} busy={busy} onRefresh={() => void look(token.trim(), remember)} onForget={forget} />
      ) : (
        <section aria-labelledby="me-form-title" className="rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:p-6">
          <h1 id="me-form-title" className="text-title-2-medium text-text-primary">
            My access
          </h1>
          <p className="mt-1 text-body-2-regular text-text-secondary">
            Paste the token you got from your link to see what you can use, how much is left and when it ends. It is sent only to this server.
          </p>
          <form onSubmit={submit} className="mt-5 flex flex-col gap-4" noValidate>
            {err && <Callout tone="error">{err}</Callout>}
            <Input
              ref={field}
              name="token"
              type="password"
              label="Your token"
              placeholder="acn_..."
              autoComplete="off"
              revealable
              isRequired
              isInvalid={!!err}
              value={token}
              onChange={(v) => {
                setToken(v);
                setErr(null);
              }}
              inputDir="ltr"
              inputClassName="font-mono"
            />
            <Checkbox isSelected={remember} onChange={setRemember}>
              <span className="flex flex-col">
                <span className="text-body-2-medium text-text-primary">Remember on this device</span>
                <span className="text-caption-1-regular text-text-secondary">Only on a device you do not share. You can forget it at any time.</span>
              </span>
            </Checkbox>
            <Button type="submit" className="w-full sm:w-auto sm:self-start" disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
              Show my access
            </Button>
            <p className="text-caption-1-regular text-text-secondary">If you were shown keys instead of a token, there is nothing to check here: you call the provider directly.</p>
          </form>
        </section>
      )}
    </PublicShell>
  );
}
