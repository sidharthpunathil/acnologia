import { useEffect, useState, type ReactNode } from "react";
import { Trash2 } from "lucide-react";
import { Button, IconButton } from "./ui";

/**
 * Inline two step confirm. First click swaps the trigger for "Confirm / Cancel".
 * Resets itself after a few seconds of no interaction.
 */
export function ConfirmButton({
  onConfirm,
  label = "Delete",
  confirmLabel = "Confirm delete",
  iconOnly,
  icon,
}: {
  onConfirm: () => Promise<void> | void;
  label?: string;
  confirmLabel?: string;
  iconOnly?: boolean;
  icon?: ReactNode;
}) {
  const [armed, setArmed] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!armed) return;
    const t = window.setTimeout(() => setArmed(false), 5000);
    return () => window.clearTimeout(t);
  }, [armed]);

  if (armed) {
    return (
      <span className="inline-flex items-center gap-1">
        <Button
          size="sm"
          variant="danger"
          loading={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await onConfirm();
            } finally {
              setBusy(false);
              setArmed(false);
            }
          }}
        >
          {confirmLabel}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setArmed(false)}>
          Cancel
        </Button>
      </span>
    );
  }

  if (iconOnly) {
    return (
      <IconButton label={label} onClick={() => setArmed(true)} className="hover:text-red-700">
        {icon ?? <Trash2 className="h-4 w-4" />}
      </IconButton>
    );
  }

  return (
    <Button size="sm" variant="ghost" icon={icon ?? <Trash2 className="h-3.5 w-3.5" />} onClick={() => setArmed(true)} className="hover:text-red-700">
      {label}
    </Button>
  );
}
