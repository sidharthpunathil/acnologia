import { RiAddLine, RiDeleteBinLine, RiKey2Line, RiPencilLine, RiPulseLine, RiWebhookLine } from "@remixicon/react";
import { ACN_ICONS, AcnAiBuilder, AcnApiKey, AcnKeyRotate, categoryIcon } from "@/components/foundations/icons/acn";
import { Button } from "@/components/base/buttons/button";
import { Tab, TabList, Tabs } from "@/components/base/tabs/tabs";
import { useTitle } from "@/hooks/useTitle";
import { cx } from "@/utils/cx";

const SIZES = [16, 20, 24, 40] as const;
const REMIX = [RiKey2Line, RiPulseLine, RiWebhookLine, RiPencilLine, RiDeleteBinLine];

/** One theme's sheet: every icon at every size, then a strip beside Remix icons. */
function Sheet({ dark }: { dark?: boolean }) {
  return (
    <section
      aria-label={dark ? "Dark theme" : "Light theme"}
      className={cx(dark && "dark", "rounded-3xl border border-border-button-default bg-background-full p-5 text-text-primary")}
    >
      <h2 className="mb-4 text-headline-semibold">{dark ? "Dark" : "Light"}</h2>
      <ul className="grid grid-cols-1 gap-x-6 gap-y-2 md:grid-cols-2">
        {ACN_ICONS.map(({ name, icon: I, meaning }) => (
          <li key={name} data-icon={name} className="flex items-center gap-4 border-b border-separator-border py-2">
            <div className="flex w-[150px] items-end gap-3 text-foreground-icon-primary">
              {SIZES.map((s) => (
                <I key={s} style={{ width: s, height: s }} />
              ))}
            </div>
            <div className="flex items-center gap-1.5 text-foreground-icon-secondary">
              <RiKey2Line className="size-5" aria-hidden />
              <I className="size-5" />
              <RiPulseLine className="size-5" aria-hidden />
            </div>
            <div className="min-w-0">
              <div className="truncate font-mono text-caption-1-medium">{name}</div>
              <div className="truncate text-caption-1-regular text-text-secondary">{meaning}</div>
            </div>
          </li>
        ))}
      </ul>
      <div className="mt-5 flex flex-wrap items-center gap-3 text-foreground-icon-secondary">
        {ACN_ICONS.map(({ name, icon: I }, i) => {
          const R = REMIX[i % REMIX.length];
          return (
            <span key={name} className="inline-flex items-center gap-1">
              <I className="size-4" />
              <R className="size-4" aria-hidden />
            </span>
          );
        })}
      </div>
      <div className="mt-5 flex flex-wrap items-center gap-2">
        <Button variant="primary" leadingIcon={RiAddLine}>
          Add service
        </Button>
        <Button variant="secondary" leadingIcon={AcnApiKey}>
          Test with key
        </Button>
        <Button variant="secondary" leadingIcon={AcnKeyRotate}>
          Rotate token
        </Button>
        <Button variant="primary" leadingIcon={AcnAiBuilder}>
          Build with AI
        </Button>
      </div>
      <Tabs className="mt-4">
        <TabList aria-label={dark ? "Sample categories, dark" : "Sample categories"}>
          {["All", "LLM", "Search", "Voice", "Image", "Other"].map((c) => (
            <Tab key={c} id={c} icon={c === "All" ? undefined : categoryIcon(c)} count={3}>
              {c}
            </Tab>
          ))}
        </TabList>
      </Tabs>
    </section>
  );
}

/** Hidden review page for the Acnologia icon set. Not linked from the nav. */
export function IconGalleryPage() {
  useTitle("Icons");
  return (
    <div className="flex flex-col gap-6 pb-10">
      <h1 className="text-title-2-medium text-text-primary">Acnologia icons</h1>
      <Sheet />
      <Sheet dark />
    </div>
  );
}
