import { useRef, useState } from "react";
import { Globe, ImageUp, Trash2 } from "lucide-react";
import type { LogoUpload } from "../lib/types";
import { errorMessage } from "../lib/api";
import { toast } from "../stores/toast";
import { EntityLogo } from "./EntityLogo";
import { Button, Input } from "./ui";

export const LOGO_MAX_BYTES = 512 * 1024;
const MIME_BY_EXT: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  webp: "image/webp",
  svg: "image/svg+xml",
  ico: "image/x-icon",
};
const ALLOWED = new Set(["image/png", "image/jpeg", "image/webp", "image/svg+xml", "image/x-icon", "image/vnd.microsoft.icon"]);

/** Reads a file as base64 after checking type and the 512 KB limit. Throws a readable message. */
export async function readLogo(file: File): Promise<LogoUpload> {
  const ext = file.name.split(".").pop()?.toLowerCase() ?? "";
  const mime = file.type && ALLOWED.has(file.type) ? file.type : MIME_BY_EXT[ext];
  if (!mime) throw new Error("Use a PNG, JPEG, WebP, SVG or ICO image.");
  if (file.size > LOGO_MAX_BYTES) throw new Error(`That image is ${Math.ceil(file.size / 1024)} KB. The limit is 512 KB.`);
  const dataUrl = await new Promise<string>((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(new Error("Could not read that file."));
    r.readAsDataURL(file);
  });
  const comma = dataUrl.indexOf(",");
  return { data_base64: comma >= 0 ? dataUrl.slice(comma + 1) : dataUrl, mime };
}

/**
 * Logo preview with upload, optional "fetch from website" and remove.
 * `hasOwn` says whether the entity has its own logo, so Remove is meaningful.
 */
export function LogoEditor({
  src,
  name,
  color,
  hasOwn,
  disabled,
  website,
  onUpload,
  onFetch,
  onRemove,
  inheritedNote,
}: {
  src: string | null | undefined;
  name: string;
  color?: string | null;
  hasOwn: boolean;
  disabled?: boolean;
  website?: string;
  onUpload: (logo: LogoUpload) => Promise<void>;
  onFetch?: (url?: string) => Promise<void>;
  onRemove: () => Promise<void>;
  inheritedNote?: string;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState<"upload" | "fetch" | "remove" | null>(null);
  const [fetchUrl, setFetchUrl] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const run = async (kind: "upload" | "fetch" | "remove", fn: () => Promise<void>, ok: string) => {
    setBusy(kind);
    setErr(null);
    try {
      await fn();
      toast.success(ok);
    } catch (e) {
      const m = errorMessage(e);
      setErr(m);
      toast.error(m);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
      <EntityLogo src={src} name={name || "?"} color={color} size="xl" />
      <div className="min-w-0 flex-1 space-y-3">
        <div className="flex flex-wrap gap-2">
          <input
            ref={input}
            type="file"
            accept=".png,.jpg,.jpeg,.webp,.svg,.ico,image/png,image/jpeg,image/webp,image/svg+xml,image/x-icon"
            className="sr-only"
            tabIndex={-1}
            aria-hidden="true"
            onChange={async (e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (!f) return;
              try {
                const logo = await readLogo(f);
                await run("upload", () => onUpload(logo), "Logo updated");
              } catch (x) {
                const m = errorMessage(x);
                setErr(m);
              }
            }}
          />
          <Button size="sm" icon={<ImageUp className="h-3.5 w-3.5" />} disabled={disabled || busy !== null} loading={busy === "upload"} onClick={() => input.current?.click()}>
            Upload image
          </Button>
          {onFetch && (
            <Button
              size="sm"
              icon={<Globe className="h-3.5 w-3.5" />}
              disabled={disabled || busy !== null || (!website && !fetchUrl.trim())}
              loading={busy === "fetch"}
              onClick={() => void run("fetch", () => onFetch(fetchUrl.trim() || undefined), "Logo fetched from the website")}
              title={!website && !fetchUrl.trim() ? "Add a website first, or paste a URL below" : undefined}
            >
              {fetchUrl.trim() ? "Fetch from URL" : "Fetch from website"}
            </Button>
          )}
          {hasOwn && (
            <Button
              size="sm"
              variant="ghost"
              icon={<Trash2 className="h-3.5 w-3.5" />}
              disabled={disabled || busy !== null}
              loading={busy === "remove"}
              onClick={() => void run("remove", onRemove, "Logo removed")}
              className="hover:text-red-600"
            >
              Remove
            </Button>
          )}
        </div>
        {onFetch && (
          <Input
            value={fetchUrl}
            onChange={(e) => setFetchUrl(e.target.value)}
            placeholder={website ? `Paste an image URL, or leave empty to use ${website}` : "Paste an image or website URL"}
            aria-label="Image or website URL to fetch the logo from"
            disabled={disabled}
            className="max-w-md font-mono text-[13px]"
          />
        )}
        {err ? (
          <p role="alert" className="text-xs text-red-700">
            {err}
          </p>
        ) : (
          <p className="text-xs text-muted">
            PNG, JPEG, WebP, SVG or ICO up to 512 KB. Square images look best.
            {!hasOwn && inheritedNote ? ` ${inheritedNote}` : ""}
          </p>
        )}
      </div>
    </div>
  );
}
