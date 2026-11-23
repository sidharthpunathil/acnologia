import { useEffect, useRef, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { RiEditBoxLine, RiExternalLinkLine, RiSparkling2Line } from "@remixicon/react";
import type { ChatItem, Service } from "../../lib/types";
import { ApiError, api, errorMessage } from "../../lib/api";
import { useAssistant } from "../../stores/assistant";
import { useAuth } from "../../stores/auth";
import { toast } from "../../stores/toast";
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Drawer, DrawerHeader } from "@/components/base/drawer/drawer";
import { Callout } from "@/components/acn/callout";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { cx } from "@/utils/cx";
import { AssistantText, ConfirmCard, ErrorItem, GUTTER, Steps, StoppedLine, CompactDivider, UserBubble } from "../assistant/items";
import { AskCard, BuildCard, Suggestions, ToolCards, useEntities } from "../assistant/cards";
import { Composer, LiveStatus, mergeAttachments, type Attachment } from "../assistant/parts";
import "../assistant/assistant.css";

/** The chat each signed in user keeps per service, so reopening the drawer picks it up again. */
const threadKey = (userId: number | undefined, serviceId: number) =>
  userId === undefined ? null : `acn.svcchat.${userId}.${serviceId}`;
function readThread(key: string | null): number | null {
  if (!key) return null;
  try {
    const v = window.localStorage.getItem(key);
    return v && /^\d+$/.test(v) ? Number(v) : null;
  } catch {
    return null;
  }
}
function writeThread(key: string | null, id: number | null) {
  if (!key) return;
  try {
    if (id === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, String(id));
  } catch {
    // Storage blocked: the drawer starts a new chat next time.
  }
}

type Row = { key: string; kind: "item"; item: ChatItem } | { key: string; kind: "tools"; items: ChatItem[] };

function toRows(items: ChatItem[]): Row[] {
  const rows: Row[] = [];
  for (const it of items) {
    const last = rows[rows.length - 1];
    if (it.kind === "tool" && last?.kind === "tools") last.items.push(it);
    else if (it.kind === "tool") rows.push({ key: it.id, kind: "tools", items: [it] });
    else rows.push({ key: it.id, kind: "item", item: it });
  }
  return rows;
}

const noDelay = () => undefined;

/** Openers that fit the page: what the provider serves, its catalog and rotation. */
function starters(service: Service): string[] {
  return [
    `Read what ${service.name} serves from its own API and fill in its catalog, with endpoints and offers`,
    `Which other providers serve the same endpoints as ${service.name}?`,
    `Turn on provider rotation for ${service.name} and the services that match it`,
  ];
}

/**
 * The Assistant, scoped to one service: a drawer on the service page whose
 * messages tell Claude which service "this" is. It shares the Assistant's
 * chats, so the same conversation opens on the Assistant page too.
 */
export function ServiceChat({ service, open, onClose }: { service: Service; open: boolean; onClose: () => void }) {
  const navigate = useNavigate();
  const storeKey = threadKey(useAuth((s) => s.user?.id), service.id);
  const { ai, items, currentId, loadingThread, sending, turn, stopping, fetch, send, confirm, answer, stop } = useAssistant();
  const openThread = useAssistant((s) => s.open);
  const [text, setText] = useState("");
  const [attachment, setAttachment] = useState<Attachment | null>(null);
  const [ready, setReady] = useState(false);
  const box = useRef<HTMLTextAreaElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const fade = useScrollFade();
  const entities = useEntities(items);

  const active = !!turn?.active;
  const busy = sending !== null || active;
  const working = (sending !== null && sending === currentId) || active;
  const off = !!ai && !ai.enabled;

  // Opening the drawer opens this service's chat, or a blank one.
  useEffect(() => {
    if (!open) {
      setReady(false);
      return;
    }
    fetch().catch(() => {});
    const saved = readThread(storeKey);
    openThread(saved)
      .catch((e) => {
        if (e instanceof ApiError && e.status === 404) writeThread(storeKey, null);
        else toast.error(errorMessage(e));
        return openThread(null);
      })
      .finally(() => setReady(true));
  }, [open, storeKey, openThread, fetch]);

  // Remember the thread once the first message made it.
  useEffect(() => {
    if (open && ready && currentId !== null) writeThread(storeKey, currentId);
  }, [open, ready, currentId, storeKey]);

  // Follow new items to the bottom.
  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, [items, working]);

  const submit = async (e?: FormEvent, override?: string) => {
    e?.preventDefault();
    const typed = override ?? text.trim();
    const msg = [typed, override === undefined ? attachment?.text.trim() : ""].filter(Boolean).join("\n\n");
    if (!msg || off) return;
    if (busy) {
      toast.info("Claude is still working on the last message.");
      return;
    }
    if (override === undefined) {
      setText("");
      setAttachment(null);
    }
    try {
      await send(msg, [], { service: service.slug });
    } catch (err) {
      if (override === undefined) setText((cur) => cur || typed);
      if (err instanceof ApiError && err.code === "busy") toast.info("Still working on your last message. Try again in a moment.");
      else toast.error(errorMessage(err));
    } finally {
      window.setTimeout(() => box.current?.focus({ preventScroll: true }), 0);
    }
  };

  const newChat = () => {
    writeThread(storeKey, null);
    void openThread(null);
    setText("");
    setAttachment(null);
    window.setTimeout(() => box.current?.focus(), 0);
  };

  const onConfirm = async (pendingId: string, approve: boolean) => {
    try {
      await confirm(pendingId, approve);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };
  const onAnswer = async (body: Parameters<typeof answer>[0]) => {
    try {
      await answer(body);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };
  const logoURL = (logoId: string) => (currentId === null ? "" : api.chatLogoURL(currentId, logoId));

  const rows = toRows(items);
  const renderRow = (row: Row, i: number) => {
    const spacing = i === 0 ? "" : "mt-4";
    if (row.kind === "tools")
      return (
        <div key={row.key} className={cx(spacing, GUTTER)}>
          <Steps items={row.items} seconds={null} fresh={noDelay} live={working && i === rows.length - 1} />
          <ToolCards items={row.items} logoURL={logoURL} />
        </div>
      );
    const it = row.item;
    let body = null;
    if (it.kind === "user") body = <UserBubble text={it.text ?? ""} keys={it.keys} keyList={it.key_list} />;
    else if (it.kind === "assistant") {
      const last = i === rows.length - 1;
      body = (
        <>
          <AssistantText item={it} entities={entities} actions={!(working && last)} />
          {last && !working && it.suggestions?.length ? (
            <Suggestions items={it.suggestions} disabled={busy || off} onPick={(s) => void submit(undefined, s)} />
          ) : null}
        </>
      );
    } else if (it.kind === "build") body = <div className={GUTTER}><BuildCard item={it} /></div>;
    else if (it.kind === "ask") body = <div className={GUTTER}><AskCard item={it} busy={busy} onAnswer={onAnswer} logoURL={logoURL} /></div>;
    else if (it.kind === "confirm")
      body = (
        <div className={GUTTER}>
          <ConfirmCard item={it} busy={busy} onAnswer={(a) => (it.pending ? onConfirm(it.pending.id, a) : Promise.resolve())} />
        </div>
      );
    else if (it.kind === "compact") body = <div className={GUTTER}><CompactDivider item={it} /></div>;
    else if (it.kind === "stopped") body = <div className={GUTTER}><StoppedLine item={it} /></div>;
    else if (it.kind === "error") body = <div className={GUTTER}><ErrorItem text={it.text} /></div>;
    if (!body) return null;
    return (
      <div key={row.key} className={spacing}>
        {body}
      </div>
    );
  };

  return (
    <Drawer isOpen={open} onOpenChange={(o) => !o && onClose()} size="lg" aria-label={`Ask AI about ${service.name}`}>
      <DrawerHeader title={`Ask AI about ${service.name}`} description="Claude knows which service you are on and acts through the dashboard as you.">
        <div className="mt-3 flex flex-wrap gap-2">
          <Button size="small" variant="secondary" leadingIcon={RiEditBoxLine} onClick={newChat} disabled={busy}>
            New chat
          </Button>
          {currentId !== null && (
            <TooltipTrigger delay={500}>
              <IconButton
                icon={RiExternalLinkLine}
                size="small"
                aria-label="Open in Assistant"
                onClick={() => {
                  onClose();
                  navigate(`/assistant/${currentId}`);
                }}
              />
              <Tooltip placement="bottom">Open in Assistant</Tooltip>
            </TooltipTrigger>
          )}
        </div>
      </DrawerHeader>
      <div className="relative min-h-0 flex-1">
        <div ref={scroller} onScroll={fade.onScroll} className="h-full overflow-y-auto overscroll-contain px-5 pt-3 pb-4">
          {!ready || loadingThread ? (
            <p className="text-body-2-regular text-text-tertiary" role="status">
              Loading chat
            </p>
          ) : items.length === 0 && !working ? (
            <div className="flex flex-col gap-3">
              {off ? (
                <Callout tone="warning">The Assistant needs an Anthropic API key. Set ANTHROPIC_API_KEY, then restart Acnologia.</Callout>
              ) : (
                <>
                  <p className="flex items-center gap-2 text-body-2-regular text-text-secondary">
                    <RiSparkling2Line className="size-4 shrink-0 text-foreground-icon-secondary" aria-hidden />
                    Ask about {service.name}, or let Claude update it.
                  </p>
                  <div className="flex flex-col gap-2">
                    {starters(service).map((s) => (
                      <button
                        key={s}
                        type="button"
                        onClick={() => void submit(undefined, s)}
                        className="rounded-xl border border-border-button-default bg-background-primary-default px-3 py-2 text-start text-body-2-regular text-text-primary outline-none transition-colors duration-150 ease-out hover:bg-background-secondary-default focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                      >
                        {s}
                      </button>
                    ))}
                  </div>
                </>
              )}
            </div>
          ) : (
            <div role="log" aria-label="Conversation" aria-live="polite" aria-relevant="additions">
              {rows.map(renderRow)}
            </div>
          )}
          {working && (
            <div className="mt-5">
              <LiveStatus step={turn?.step} startedAt={turn?.started_at} />
            </div>
          )}
        </div>
        <ScrollFade progress={fade.progress} surfaceClassName="from-background-primary-default" />
      </div>
      <div className="shrink-0 px-3 pt-1 pb-3">
        <Composer
          ref={box}
          value={text}
          onChange={setText}
          onSubmit={(e) => void submit(e)}
          busy={busy && currentId === null}
          working={working}
          onStop={currentId !== null && !off ? () => void stop().catch((err) => toast.error(errorMessage(err))) : undefined}
          stopping={stopping !== null && stopping === currentId}
          off={off}
          model={ai?.model}
          attachment={attachment}
          onAttach={(a) => setAttachment((cur) => mergeAttachments(cur, a))}
          onClearAttachment={() => setAttachment(null)}
        />
      </div>
    </Drawer>
  );
}
