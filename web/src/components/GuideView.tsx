import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { RiRefreshLine, RiRobot2Line } from "@remixicon/react";
import { errorMessage } from "../lib/api";
import type { GuideResponse } from "../lib/types";
import { useTheme } from "../stores/theme";
import { toast } from "../stores/toast";
import { Markdown } from "../pages/docs/Markdown";
import { extractHeadings } from "../pages/docs/headings";
import { Callout, Section, copyText } from "@/components/acn";
import { Button } from "@/components/base/buttons/button";
import { SpinnerGlyph } from "@/components/base/spinner/spinner";

type Load = { state: "loading" } | { state: "ready"; data: GuideResponse } | { state: "error"; message: string };

/** Fetches a guide and refetches when `deps` changes (a new version, another service). */
export function useGuide(fetcher: () => Promise<GuideResponse>, key: string) {
  const [load, setLoad] = useState<Load>({ state: "loading" });
  const run = useCallback(async () => {
    setLoad((l) => (l.state === "ready" ? l : { state: "loading" }));
    try {
      setLoad({ state: "ready", data: await fetcher() });
    } catch (e) {
      setLoad({ state: "error", message: errorMessage(e) });
    }
    // The key names everything the fetcher depends on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  useEffect(() => {
    void run();
  }, [run]);
  return { load, reload: run };
}

/**
 * A usage guide rendered as Markdown, with "Copy for LLM", which copies the
 * Markdown as is. Used on the provider page and a service's Integrate tab.
 */
export function GuideView({
  title = "Usage guide",
  description,
  fetcher,
  fetchKey,
  generatedHint,
  actions,
}: {
  title?: string;
  description?: string;
  fetcher: () => Promise<GuideResponse>;
  /** Changes whenever the guide should be fetched again. */
  fetchKey: string;
  /** What to tell the reader when the guide was generated rather than written. */
  generatedHint?: string;
  /** More controls before Copy for LLM. */
  actions?: ReactNode;
}) {
  const { load, reload } = useGuide(fetcher, fetchKey);
  const theme = useTheme((s) => s.theme);
  const md = load.state === "ready" ? load.data.markdown : "";
  const headings = useMemo(() => extractHeadings(md), [md]);

  const copy = async () => {
    if (await copyText(md)) toast.success("Copied. Paste it into your LLM.");
    else toast.error("Could not copy. Select the text in the guide instead.");
  };

  return (
    <Section
      title={title}
      description={description}
      actions={
        <>
          {actions}
          <Button variant="secondary" size="small" leadingIcon={RiRobot2Line} onClick={() => void copy()} disabled={load.state !== "ready"}>
          Copy for LLM
          </Button>
        </>
      }
    >
      {load.state === "loading" && (
        <div className="flex items-center gap-2 text-body-2-regular text-text-secondary">
          <SpinnerGlyph size="sm" />
          Loading the guide
        </div>
      )}
      {load.state === "error" && (
        <Callout
          tone="error"
          action={
            <Button size="small" variant="secondary" leadingIcon={RiRefreshLine} onClick={() => void reload()}>
              Try again
            </Button>
          }
        >
          Could not load the guide. {load.message}
        </Callout>
      )}
      {load.state === "ready" && (
        <div className="flex flex-col gap-5">
          {load.data.generated && (
            <Callout tone="info">
              {generatedHint ?? "Acnologia wrote this guide from the provider's examples, offers and docs link. A written guide explains endpoints and parameters better."}
            </Callout>
          )}
          <div className="min-w-0 [&_h2]:mt-8! [&_h2]:pt-6!">
            <Markdown source={md} headings={headings} theme={theme} />
          </div>
        </div>
      )}
    </Section>
  );
}
