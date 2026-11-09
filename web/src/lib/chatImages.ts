/**
 * Images for the Assistant: pasted, dropped or picked pictures, scaled down
 * in the browser before they are sent. Claude reads an image at full detail
 * up to about 1568px on its long side, so anything bigger only costs upload
 * time and tokens.
 */

/** An image waiting in the composer. Memory only, like the rest of a draft. */
export interface DraftImage {
  id: string;
  name: string;
  mediaType: "image/png" | "image/jpeg" | "image/gif" | "image/webp";
  /** Base64, no data: prefix. */
  data: string;
  /** A data: URL for the preview. */
  url: string;
}

export const MAX_IMAGES = 4;
const MAX_EDGE = 1568;
/** The server takes up to 700 KB per image; stay under it with room to spare. */
const MAX_BYTES = 600 * 1024;
const ACCEPTED = ["image/png", "image/jpeg", "image/gif", "image/webp"];

let seq = 0;

export function isImageFile(f: File): boolean {
  return f.type.startsWith("image/");
}

function toDataURL(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error ?? new Error("could not read the image"));
    r.readAsDataURL(blob);
  });
}

function encode(canvas: HTMLCanvasElement, type: string, quality?: number): Promise<Blob> {
  return new Promise((resolve, reject) =>
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("could not encode the image"))), type, quality),
  );
}

/**
 * Prepares one image file: kept as is when it is an accepted type, small
 * enough and not too large in pixels; otherwise drawn onto a canvas at most
 * MAX_EDGE on its long side and saved as PNG (when that stays small, for
 * screenshots with text) or JPEG.
 */
export async function prepareImage(file: File): Promise<DraftImage> {
  const bitmap = await createImageBitmap(file).catch(() => {
    throw new Error(`${file.name || "That file"} is not an image the browser can read`);
  });
  const { width, height } = bitmap;
  const scale = Math.min(1, MAX_EDGE / Math.max(width, height));
  const keep = ACCEPTED.includes(file.type) && scale === 1 && file.size <= MAX_BYTES;
  let blob: Blob = file;
  if (!keep) {
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(width * scale));
    canvas.height = Math.max(1, Math.round(height * scale));
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("could not scale the image");
    ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    blob = await encode(canvas, "image/png");
    for (const q of [0.9, 0.8, 0.65, 0.5]) {
      if (blob.size <= MAX_BYTES) break;
      blob = await encode(canvas, "image/jpeg", q);
    }
    if (blob.size > MAX_BYTES) throw new Error(`${file.name || "That image"} is too large even after scaling it down`);
  }
  bitmap.close();
  const url = await toDataURL(blob);
  const mediaType = (blob.type || file.type) as DraftImage["mediaType"];
  return { id: `img-${++seq}`, name: file.name || "Pasted image", mediaType, data: url.slice(url.indexOf(",") + 1), url };
}

/** Prepares files, at most `room` of them; errors come back as messages. */
export async function prepareImages(files: File[], room: number): Promise<{ images: DraftImage[]; errors: string[] }> {
  const images: DraftImage[] = [];
  const errors: string[] = [];
  const list = files.filter(isImageFile);
  if (list.length > room) errors.push(`At most ${MAX_IMAGES} images per message; the rest were left out.`);
  for (const f of list.slice(0, Math.max(0, room))) {
    try {
      images.push(await prepareImage(f));
    } catch (e) {
      errors.push((e as Error).message);
    }
  }
  return { images, errors };
}
