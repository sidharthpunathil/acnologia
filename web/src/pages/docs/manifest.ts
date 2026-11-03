import {
  RiBookOpenLine,
  RiCodeSSlashLine,
  RiCodeBoxLine,
  RiCpuLine,
  RiGitBranchLine,
  RiPuzzle2Line,
  RiRocket2Line,
  RiServerLine,
  RiSettings3Line,
  RiShieldCheckLine,
  RiTerminalBoxLine,
  RiToolsLine,
  RiTrainLine,
} from "@remixicon/react";
import type { IconComponent } from "@/components/acn/types";

export interface DocEntry {
  slug: string;
  title: string;
  /** One line for the sidebar, the Guides grid and llms.txt. */
  blurb: string;
  icon: IconComponent;
  /** The sidebar section it sits under. */
  group: DocGroupId;
}

export type DocGroupId = "start" | "concepts" | "operate" | "reference" | "contribute";

/** Sidebar sections, in reading order. */
export const DOC_GROUPS: Array<{ id: DocGroupId; title: string; blurb: string }> = [
  { id: "start", title: "Get started", blurb: "What Acnologia is and how to run it." },
  { id: "concepts", title: "How it works", blurb: "The server, its request paths and its security model." },
  { id: "operate", title: "Run it", blurb: "Settings, deploying, backups and upgrades." },
  { id: "reference", title: "Reference", blurb: "The HTTP API and the provider spec format." },
  { id: "contribute", title: "Contribute", blurb: "Work on Acnologia itself." },
];

/**
 * The docs in reading order. Mirrors `pages` in server/internal/docsmd, which
 * serves each one at /docs/<slug>.md. Kept here so navigation renders without
 * waiting on /api/docs.
 */
export const DOCS: DocEntry[] = [
  { slug: "overview", title: "Overview", blurb: "What Acnologia does, quick start and features.", icon: RiBookOpenLine, group: "start" },
  { slug: "architecture", title: "Server architecture", blurb: "Components, packages, request pipeline, data model and background work.", icon: RiCpuLine, group: "concepts" },
  { slug: "flows", title: "Request flows", blurb: "Proxy requests, leases, key states and outcomes, MCP, the AI builder, the Assistant, sharing and the provider refresh.", icon: RiGitBranchLine, group: "concepts" },
  { slug: "security", title: "Security model", blurb: "Roles, hidden keys, what the model can see, secrets at rest, outbound requests and sharing between servers.", icon: RiShieldCheckLine, group: "concepts" },
  { slug: "configuration", title: "Configuration", blurb: "Environment variables, per service settings, self-hosting, Windows, backups and upgrading.", icon: RiSettings3Line, group: "operate" },
  { slug: "deploy", title: "Deploying", blurb: "Every way to deploy: one click on Render or Railway, Fly.io, the container image and the settings any public deploy needs.", icon: RiRocket2Line, group: "operate" },
  { slug: "deploy-railway", title: "Deploying on Railway", blurb: "A public deploy behind the access password, with a volume, a template, Infrastructure as Code, AI and sharing.", icon: RiTrainLine, group: "operate" },
  { slug: "deploy-vps", title: "Deploying on a server", blurb: "One script for any Linux server with HTTPS, cloud-init, Hetzner and DigitalOcean in one command, backups and upgrades.", icon: RiServerLine, group: "operate" },
  { slug: "api", title: "HTTP API", blurb: "Consumer error codes and every dashboard route with its required role.", icon: RiCodeSSlashLine, group: "reference" },
  { slug: "cli", title: "Command line", blurb: "Install acn, sign in through the browser, every command and CI variables.", icon: RiTerminalBoxLine, group: "reference" },
  { slug: "sdks", title: "SDKs", blurb: "The twelve client libraries, how to install each today and how to run their tests.", icon: RiCodeBoxLine, group: "reference" },
  { slug: "providers", title: "Provider specs", blurb: "The built in providers, the spec format, offers and refresh, and adding one by hand, with AI or from another server.", icon: RiPuzzle2Line, group: "reference" },
  { slug: "development", title: "Development", blurb: "Setup, repository layout, tests and conventions.", icon: RiToolsLine, group: "contribute" },
];

export const docBySlug = (slug: string | undefined) => DOCS.find((d) => d.slug === slug);

/** The raw Markdown URL for a doc. Served by the backend, never by the SPA. */
export const mdUrl = (slug: string) => `/docs/${slug}.md`;

export const docsInGroup = (id: DocGroupId) => DOCS.filter((d) => d.group === id);

export const groupOf = (doc: DocEntry) => DOC_GROUPS.find((g) => g.id === doc.group)!;
