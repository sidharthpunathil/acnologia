import { RiChat3Line, RiDeleteBinLine, RiMore2Fill } from "@remixicon/react";
import { useRef, useState } from "react";

import { EmptyState } from "@/components/acn/empty-state";
import { StatusDot } from "@/components/base/badges/status-dot";
import {
  Dropdown,
  DropdownGroup,
  DropdownItem,
  DropdownPopover,
  DropdownTrigger,
} from "@/components/base/dropdown/dropdown";
import { AlertDialog } from "@/components/base/modal/modal";
import { cx } from "@/utils/cx";

/**
 * The chat history list, adapted from BoardUI's free Chat Starter rail.
 *
 * Acnologia shows it inside the Chats dialog rather than as a rail beside
 * the chat, so the starter's card, account strip and export control are
 * gone. What stays is the row: the title, the age and the row menu sharing
 * one trailing slot, so the menu appears in place without moving the title.
 * Delete asks first in an AlertDialog. A thread with a turn still running
 * carries a pulsing dot in front of its title.
 */

export interface ChatThreadSummary {
  id: number;
  title: string;
  /** Compact age such as "now", "34m", "5h", "3d". */
  age: string;
  /** A turn is still running in this thread. */
  running?: boolean;
}

export interface AgentChatHistoryProps {
  threads: ChatThreadSummary[];
  activeId: number | null;
  onSelect: (id: number) => void;
  /** Resolves once deleted; rejects to keep the confirmation open. */
  onDelete: (id: number) => Promise<void>;
  className?: string;
}

export function AgentChatHistory({ threads, activeId, onSelect, onDelete, className }: AgentChatHistoryProps) {
  const [deleting, setDeleting] = useState<ChatThreadSummary | null>(null);
  const list = useRef<HTMLUListElement>(null);
  // The deleted row took its menu trigger with it, so focus would fall to the
  // page and Escape would no longer reach the dialog. Land on the next row.
  const refocus = () =>
    window.setTimeout(() => list.current?.querySelector<HTMLButtonElement>("button")?.focus(), 320);

  if (threads.length === 0) {
    return (
      <EmptyState
        compact
        icon={RiChat3Line}
        title="No chats yet"
        body="Each one you start is kept here."
        className={className}
      />
    );
  }

  return (
    <>
      <ul ref={list} className={cx("flex flex-col gap-0.5", className)} aria-label="Chats">
        {threads.map((thread) => (
          <ThreadRow
            key={thread.id}
            thread={thread}
            active={thread.id === activeId}
            onSelect={onSelect}
            onDelete={() => setDeleting(thread)}
          />
        ))}
      </ul>
      <AlertDialog
        isOpen={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
        title="Delete this chat?"
        description={deleting ? `"${deleting.title || "New chat"}" and its messages are removed for good.` : undefined}
        confirmLabel="Delete"
        pendingLabel="Deleting"
        onConfirm={() => (deleting ? onDelete(deleting.id).then(refocus) : undefined)}
      />
    </>
  );
}

function ThreadRow({
  thread,
  active,
  onSelect,
  onDelete,
}: {
  thread: ChatThreadSummary;
  active: boolean;
  onSelect: (id: number) => void;
  onDelete: () => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const title = thread.title || "New chat";

  return (
    <li
      className={cx(
        "group/row relative flex items-center rounded-2lg transition-colors duration-150 ease-out",
        active || menuOpen ? "bg-background-secondary-hover" : "hover:bg-background-secondary-default",
      )}
    >
      <button
        type="button"
        onClick={() => onSelect(thread.id)}
        aria-current={active ? "page" : undefined}
        className="flex min-h-11 min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-2lg py-2 ps-3 text-start outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
      >
        {thread.running ? (
          <>
            <StatusDot color="green" className="shrink-0 animate-pulse motion-reduce:animate-none" />
            <span className="sr-only">Working: </span>
          </>
        ) : null}
        <span
          className={cx(
            "min-w-0 flex-1 truncate text-body-2-regular",
            active ? "text-text-primary text-body-2-medium" : "text-text-secondary",
          )}
        >
          {title}
        </span>
      </button>

      {/* The age and the menu share one slot. With a mouse the age steps aside
          on hover or keyboard focus so the control appears in place; touch
          screens always show the menu. */}
      <span className="group/slot relative flex size-11 shrink-0 items-center justify-center">
        <span
          aria-hidden
          className={cx(
            "hidden font-mono text-caption-1-regular text-text-tertiary transition-opacity duration-150 [@media(hover:hover)]:inline",
            menuOpen ? "opacity-0" : "opacity-100 group-hover/row:opacity-0 group-has-[:focus-visible]/slot:opacity-0",
          )}
        >
          {thread.age}
        </span>
        <span
          className={cx(
            "absolute inset-0 flex items-center justify-center transition-opacity duration-150",
            menuOpen
              ? "opacity-100"
              : "[@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover/row:opacity-100 [@media(hover:hover)]:group-has-[:focus-visible]/slot:opacity-100",
          )}
        >
          <Dropdown isOpen={menuOpen} onOpenChange={setMenuOpen}>
            <DropdownTrigger
              aria-label={`More actions for ${title}`}
              className={cx(
                "inline-flex size-8 shrink-0 items-center justify-center rounded-lg",
                "text-foreground-icon-secondary transition-colors duration-150 ease-out",
                "hover:bg-background-tertiary-default hover:text-foreground-icon-primary",
                "outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring",
                menuOpen && "bg-background-tertiary-default text-foreground-icon-primary",
              )}
            >
              <RiMore2Fill className="size-4 shrink-0" aria-hidden />
            </DropdownTrigger>
            <DropdownPopover aria-label={`More actions for ${title}`} placement="bottom end" className="w-[190px] p-2">
              <DropdownGroup>
                <DropdownItem
                  onSelect={() => {
                    setMenuOpen(false);
                    onDelete();
                  }}
                  className="px-2 py-1.5"
                >
                  <RiDeleteBinLine className="size-[18px] shrink-0 text-text-error-primary" aria-hidden />
                  <span className="truncate text-body-medium whitespace-nowrap text-text-error-primary">Delete</span>
                </DropdownItem>
              </DropdownGroup>
            </DropdownPopover>
          </Dropdown>
        </span>
      </span>
    </li>
  );
}

/** Compact ages for the narrow slot: now, 34m, 5h, 18h, 3d. */
export function compactAge(iso: string, now = Date.now()) {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return "";
  const seconds = Math.max(0, Math.round((now - at) / 1000));
  if (seconds < 60) return "now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}
