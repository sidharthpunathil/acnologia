import { CopyButton as AcnCopyButton, SecretBox, copyText } from "@/components/acn/copy-button";

export { SecretBox, copyText };

/** Legacy props over acn/copy-button. `dark` means it sits on the code surface. */
export function CopyButton({ text, label = "Copy", className, dark }: { text: string; label?: string; className?: string; dark?: boolean }) {
  return <AcnCopyButton text={text} label={label} className={className} onCode={dark} />;
}
