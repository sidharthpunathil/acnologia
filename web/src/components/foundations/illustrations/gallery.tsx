import { cx } from "@/utils/cx";
import { useTitle } from "@/hooks/useTitle";
import { EmptyState } from "@/components/acn/empty-state";
import type { Illustration } from "./frame";
import {
  FallbackArt,
  KeyCheckArt,
  KeyRingArt,
  LostPathArt,
  PeopleArt,
  PulseArt,
  SearchArt,
  ShelfArt,
  SpecSheetArt,
  TokenCardArt,
  UnpluggedArt,
} from "./art";

const PIECES: { name: string; use: string; Art: Illustration }[] = [
  { name: "KeyRingArt", use: "No keys yet", Art: KeyRingArt },
  { name: "ShelfArt", use: "No services", Art: ShelfArt },
  { name: "TokenCardArt", use: "No apps", Art: TokenCardArt },
  { name: "PulseArt", use: "No activity", Art: PulseArt },
  { name: "SpecSheetArt", use: "No runs, no providers", Art: SpecSheetArt },
  { name: "SearchArt", use: "No search results", Art: SearchArt },
  { name: "UnpluggedArt", use: "No webhooks or egress", Art: UnpluggedArt },
  { name: "FallbackArt", use: "No service groups", Art: FallbackArt },
  { name: "PeopleArt", use: "Just you so far", Art: PeopleArt },
  { name: "KeyCheckArt", use: "All good", Art: KeyCheckArt },
  { name: "LostPathArt", use: "404, not found", Art: LostPathArt },
];

function Board({ dark }: { dark?: boolean }) {
  return (
    <section className={cx("rounded-3xl bg-background-full p-4 sm:p-6", dark && "dark")} data-testid={dark ? "gallery-dark" : "gallery-light"}>
      <h2 className="mb-4 font-mono text-body-2-semibold text-text-primary">{dark ? "Dark" : "Light"}</h2>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
        {PIECES.map(({ name, use, Art }) => (
          <figure key={name} className="flex flex-col items-center rounded-2xl border border-border-button-default bg-background-primary-default p-4">
            <Art className="h-auto w-full max-w-[160px]" />
            <figcaption className="mt-3 text-center">
              <span className="block font-mono text-caption-1-regular text-text-primary">{name}</span>
              <span className="block text-caption-2-regular text-text-tertiary">{use}</span>
            </figcaption>
          </figure>
        ))}
      </div>
      <div className="mt-4 grid gap-3 sm:grid-cols-2">
        <EmptyState art={KeyRingArt} title="No keys yet" body="EmptyState with art, full size." />
        <EmptyState compact art={SpecSheetArt} title="No runs yet" body="EmptyState with art, compact." />
      </div>
    </section>
  );
}

/** Hidden review page at /dev/illustrations: every piece on both themes. */
export function IllustrationGallery() {
  useTitle("Illustrations");
  return (
    <div className="flex flex-col gap-6">
      <Board />
      <Board dark />
    </div>
  );
}
