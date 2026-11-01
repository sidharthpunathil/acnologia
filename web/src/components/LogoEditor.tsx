import { useRef, useState } from "react";
import { RiDeleteBinLine, RiGlobalLine, RiImageAddLine } from "@remixicon/react";
import type { LogoUpload } from "../lib/types";
import { errorMessage } from "../lib/api";
import { toast } from "../stores/toast";
import { EntityLogo } from "./EntityLogo";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { ButtonSpinner } from "@/components/base/spinner/spinner";

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
    <div className="flex min-w-0 flex-col gap-4 sm:flex-row sm:items-start">
      <EntityLogo src={src} name={name || "?"} color={color} size="xl" />
      <div className="flex min-w-0 flex-1 flex-col gap-3">
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
          <Button
            variant="secondary"
            size="small"
            leadingIcon={busy === "upload" ? ButtonSpinner : RiImageAddLine}
            disabled={disabled || busy !== null}
            onClick={() => input.current?.click()}
          >
            Upload image
          </Button>
          {onFetch && (
            <Button
              variant="secondary"
              size="small"
              leadingIcon={busy === "fetch" ? ButtonSpinner : RiGlobalLine}
              disabled={disabled || busy !== null || (!website && !fetchUrl.trim())}
              onClick={() => void run("fetch", () => onFetch(fetchUrl.trim() || undefined), "Logo fetched from the website")}
              title={!website && !fetchUrl.trim() ? "Add a website first, or paste a URL below" : undefined}
            >
              {fetchUrl.trim() ? "Fetch from URL" : "Fetch from website"}
            </Button>
          )}
          {hasOwn && (
            <Button
              size="small"
              variant="ghost"
              leadingIcon={busy === "remove" ? ButtonSpinner : RiDeleteBinLine}
              disabled={disabled || busy !== null}
              onClick={() => void run("remove", onRemove, "Logo removed")}
              className="hover:text-text-error-primary"
            >
              Remove
            </Button>
          )}
        </div>
        {onFetch && (
          <Input
            value={fetchUrl}
            onChange={setFetchUrl}
            placeholder={website ? `Paste an image URL, or leave empty to use ${website}` : "Paste an image or website URL"}
            aria-label="Image or website URL to fetch the logo from"
            isDisabled={disabled}
            inputDir="ltr"
            className="max-w-md"
            inputClassName="font-mono text-body-2-regular"
          />
        )}
        {err ? (
          <p role="alert" className="text-caption-1-medium text-text-error-primary">
            {err}
          </p>
        ) : (
          <p className="text-caption-1-regular text-text-secondary">
            PNG, JPEG, WebP, SVG or ICO up to 512 KB. Square images look best.
            {!hasOwn && inheritedNote ? ` ${inheritedNote}` : ""}
          </p>
        )}
      </div>
    </div>
  );
}
