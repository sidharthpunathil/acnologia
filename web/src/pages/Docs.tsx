import { useEffect, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import { RiArrowRightLine, RiArrowRightUpLine, RiFileTextLine, RiPlugLine, RiRobot2Line, RiSearchLine } from "@remixicon/react";
import { useTitle } from "@/hooks/useTitle";
import { useAuth } from "@/stores/auth";
import { CodeBlock } from "@/components/CodeBlock";
import { Kbd } from "@/components/base/kbd/kbd";
import type { IconComponent } from "@/components/acn/types";
import { cx } from "@/utils/cx";
import { DocsLayout, SEARCH_SHORTCUT, useOpenDocsSearch } from "./docs/DocsLayout";
import { DOC_GROUPS, docsInGroup } from "./docs/manifest";
import { RouterButton } from "./docs/RouterButton";

const RUN = `git clone https://github.com/sidharthpunathil/acnologia && cd acnologia
cp .env.example .env
docker compose up -d --build
open http://localhost:8080    # the first visit creates the superadmin`;

const CALL = `curl "http://localhost:8080/proxy/serpapi/search.json?q=coffee" \\
  -H "Authorization: Bearer cog_your_app_token"`;

const CARD = "rounded-3xl border border-border-button-default bg-background-primary-default shadow-xs";
const CARD_LINK = cx(
  CARD,
  "group button-press-motion outline-none transition-[border-color,background-color] duration-150 ease-out",
  "hover:border-border-button-hover hover:bg-background-primary-hover focus-visible:ring-2 focus-visible:ring-border-focus-ring",
);

const FOCUS = "outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring";

/** An in text link, in the accent. */
function TextLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link to={to} className={cx("rounded-sm text-text-primary underline decoration-chart-1 underline-offset-4 transition-[text-decoration-color] duration-150 hover:decoration-current", FOCUS)}>
      {children}
    </Link>
  );
}

function Code({ children }: { children: ReactNode }) {
  return <code className="rounded-md bg-background-tertiary-default px-1.5 py-0.5 font-mono text-body-2-regular text-text-primary">{children}</code>;
}

function SectionTitle({ id, title, children }: { id: string; title: string; children?: ReactNode }) {
  return (
    <div>
      <h2 id={id} className="scroll-mt-24 text-title-2-semibold text-text-primary">
        {title}
      </h2>
      {children ? <p className="mt-2 max-w-[38rem] text-body-regular text-pretty text-text-secondary sm:text-headline-regular">{children}</p> : null}
    </div>
  );
}

function Step({ n, title, children }: { n: number; title: string; children: ReactNode }) {
  return (
    <li className="relative grid grid-cols-[2rem_minmax(0,1fr)] gap-x-4 pb-10 last:pb-0">
      {/* The rail that joins the steps. */}
      <span aria-hidden className="absolute start-4 top-9 bottom-1 w-px bg-separator-border [li:last-child>&]:hidden" />
      <span
        aria-hidden
        className="relative flex size-8 items-center justify-center rounded-full border border-border-button-default bg-background-primary-default font-mono text-caption-1-semibold text-text-primary shadow-xs"
      >
        {n}
      </span>
      <div className="min-w-0 pt-1">
        <h3 className="text-headline-semibold text-text-primary">{title}</h3>
        <div className="mt-1.5 text-body-regular text-text-secondary">{children}</div>
      </div>
    </li>
  );
}

function AgentLink({ href, icon: Icon, title, body }: { href: string; icon: IconComponent; title: string; body: string }) {
  // Rendered doc pages stay in the app; raw files are served by the server.
  const external = !href.startsWith("/docs/") || href.endsWith(".md");
  const inner = (
    <>
      <Icon className="size-[18px] shrink-0 text-foreground-icon-secondary" aria-hidden />
      <span className="min-w-0 flex-1">
        <span className="block font-mono text-body-medium text-text-primary">{title}</span>
        <span className="mt-0.5 block text-body-2-regular text-text-secondary">{body}</span>
      </span>
      <RiArrowRightUpLine className="size-4 shrink-0 text-foreground-icon-tertiary transition-colors duration-150 group-hover:text-foreground-icon-primary" aria-hidden />
    </>
  );
  const cls = cx(CARD_LINK, "flex h-full items-start gap-3 p-4");
  return external ? (
    <a href={href} className={cls}>
      {inner}
    </a>
  ) : (
    <Link to={href} className={cls}>
      {inner}
    </Link>
  );
}

/** The docs home: what Acnologia is, how to start, and every section as a card. */
export function DocsPage() {
  useTitle("Docs");
  const signedIn = useAuth((s) => s.status === "authenticated");
  return (
    <DocsLayout>
      <DocsHome signedIn={signedIn} />
    </DocsLayout>
  );
}

/** Lives inside DocsLayout, which owns the search dialog. */
function DocsHome({ signedIn }: { signedIn: boolean }) {
  const openSearch = useOpenDocsSearch();
  const { hash } = useLocation();
  // In app links such as /docs#group-reference land on their section.
  useEffect(() => {
    if (hash) document.getElementById(decodeURIComponent(hash.slice(1)))?.scrollIntoView({ block: "start" });
  }, [hash]);
  return (
    <main id="doc-content" tabIndex={-1} className="min-w-0 pt-8 pb-24 outline-none lg:pt-12">
      <div className="max-w-[56rem]">
        {/* Intro */}
        <section aria-labelledby="docs-title">
          <p className="text-caption-1-semibold text-status-purple-text">Documentation</p>
          <h1 id="docs-title" className="mt-3 text-title-1-semibold text-balance text-text-primary sm:text-display-4-semibold">
            One endpoint in front of all your API keys
          </h1>
          <p className="mt-4 max-w-[38rem] text-headline-regular text-pretty text-text-secondary">
            Acnologia keeps many keys per provider and sends each request with one that has quota left. When a key is rate
            limited, out of quota or suspended, it parks that key and retries with the next. One Go binary and SQLite, on your
            own server.
          </p>
          <button
            type="button"
            onClick={openSearch}
            aria-haspopup="dialog"
            className={cx(
              "mt-7 flex h-12 w-full max-w-[32rem] cursor-pointer items-center gap-3 rounded-full border border-border-button-default bg-background-primary-default ps-4 pe-2 shadow-xs",
              "text-body-regular text-text-tertiary transition-colors duration-150 hover:border-border-button-hover hover:text-text-secondary sm:text-headline-regular",
              FOCUS,
            )}
          >
            <RiSearchLine className="size-5 shrink-0" aria-hidden />
            <span className="flex-1 text-start">Search pages and sections</span>
            <Kbd className="hidden px-2 sm:inline-flex">{SEARCH_SHORTCUT}</Kbd>
          </button>
          <div className="mt-4 flex flex-wrap gap-2">
            <RouterButton to="/docs/overview" trailingIcon={RiArrowRightLine}>
              Read the overview
            </RouterButton>
            <RouterButton to="/docs/api" variant="secondary">
              HTTP API reference
            </RouterButton>
          </div>
        </section>

        {/* Quick start */}
        <section aria-labelledby="quick-start" className="mt-16 border-t border-separator-border pt-10">
          <SectionTitle id="quick-start" title="Quick start">
            From a clone to your first proxied request in three steps. <TextLink to="/docs/configuration">Configuration</TextLink>{" "}
            covers every setting.
          </SectionTitle>
          <ol className="mt-8">
            <Step n={1} title="Run the server">
              <p>Docker Compose is the quickest way. The first visit to the dashboard creates the superadmin.</p>
              <CodeBlock label="shell" code={RUN} className="mt-3" />
            </Step>
            <Step n={2} title="Add a service, its keys and an app">
              <p>
                Pick a provider from the catalog (or describe any HTTP API), paste your keys one per line, then create an app for
                each consumer and copy its <Code>cog_</Code> token. It is shown once.
              </p>
              <div className="mt-3">
                <RouterButton to={signedIn ? "/" : "/login"} size="small" variant="secondary" trailingIcon={RiArrowRightLine}>
                  {signedIn ? "Open the catalog" : "Sign in to the dashboard"}
                </RouterButton>
              </div>
            </Step>
            <Step n={3} title="Call through Acnologia">
              <p>
                Call <Code>{"/proxy/{service}/..."}</Code> as you would call the provider. Acnologia attaches a key with quota
                left. To hold the raw key yourself, see <TextLink to="/docs/flows#lease-and-report">lease and report</TextLink>.
              </p>
              <CodeBlock label="curl" code={CALL} className="mt-3" />
            </Step>
          </ol>
        </section>

        {/* Sections */}
        <section aria-labelledby="browse" className="mt-16 border-t border-separator-border pt-10">
          <SectionTitle id="browse" title="Browse the docs" />
          {/* Every page as a card, in reading order, each tagged with its sidebar section. */}
          <ul className="mt-6 grid gap-3 sm:grid-cols-2">
            {DOC_GROUPS.flatMap((g) =>
              docsInGroup(g.id).map((d, i) => (
                <li key={d.slug} id={i === 0 ? `group-${g.id}` : undefined} className="min-w-0 scroll-mt-24">
                  <Link to={`/docs/${d.slug}`} className={cx(CARD_LINK, "flex h-full items-start gap-3.5 p-4 sm:p-5")}>
                    <span className="flex size-9 shrink-0 items-center justify-center rounded-xl border border-border-button-default bg-background-secondary-default text-foreground-icon-secondary transition-colors duration-150 group-hover:text-status-purple-text">
                      <d.icon className="size-[18px]" aria-hidden />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block text-caption-1-medium text-text-tertiary">{g.title}</span>
                      <span className="mt-0.5 flex items-center gap-1 text-headline-semibold text-text-primary">
                        {d.title}
                        <RiArrowRightLine
                          className="size-4 shrink-0 text-foreground-icon-tertiary transition-transform duration-150 group-hover:translate-x-0.5 motion-reduce:transition-none"
                          aria-hidden
                        />
                      </span>
                      <span className="mt-1 block text-body-regular text-text-secondary">{d.blurb}</span>
                    </span>
                  </Link>
                </li>
              )),
            )}
          </ul>
        </section>

        {/* Agents */}
        <section aria-labelledby="for-agents" className="mt-16 border-t border-separator-border pt-10">
          <SectionTitle id="for-agents" title="For agents and scripts">
            Every page is plain Markdown too, so an agent can read the docs without a browser.
          </SectionTitle>
          <ul className="mt-6 grid gap-3 sm:grid-cols-2">
            <li>
              <AgentLink href="/llms.txt" icon={RiRobot2Line} title="/llms.txt" body="An index of every page, for LLMs." />
            </li>
            <li>
              <AgentLink href="/llms-full.txt" icon={RiFileTextLine} title="/llms-full.txt" body="All the docs in one file." />
            </li>
            <li>
              <AgentLink href="/docs/overview.md" icon={RiFileTextLine} title="/docs/{page}.md" body="Any single page as raw Markdown." />
            </li>
            <li>
              <AgentLink href="/docs/flows#mcp" icon={RiPlugLine} title="POST /mcp" body="Lease keys and proxy requests from an MCP client." />
            </li>
          </ul>
        </section>
      </div>
    </main>
  );
}
