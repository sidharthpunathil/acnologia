import {
  RiBookOpenLine,
  RiCodeSSlashLine,
  RiCpuLine,
  RiGitBranchLine,
  RiPuzzle2Line,
  RiSettings3Line,
  RiShieldCheckLine,
  RiToolsLine,
} from "@remixicon/react";
import type { IconComponent } from "@/components/acn/types";

export interface DocEntry {
  slug: string;
  title: string;
  /** One line for the sidebar, the Guides grid and llms.txt. */
  blurb: string;
  icon: IconComponent;
}

/**
 * The docs in reading order. Mirrors `pages` in server/internal/docsmd, which
 * serves each one at /docs/<slug>.md. Kept here so navigation renders without
 * waiting on /api/docs.
 */
export const DOCS: DocEntry[] = [
  { slug: "overview", title: "Overview", blurb: "What Acnologia does, quick start, features and self-hosting.", icon: RiBookOpenLine },
  { slug: "architecture", title: "Server architecture", blurb: "Components, packages, request pipeline, data model and background work.", icon: RiCpuLine },
  { slug: "flows", title: "Request flows", blurb: "Diagrams of proxy requests, key selection, leases, key states and sign in.", icon: RiGitBranchLine },
  { slug: "security", title: "Security model", blurb: "Roles, hidden keys, secrets at rest, web protections and outbound requests.", icon: RiShieldCheckLine },
  { slug: "configuration", title: "Configuration", blurb: "Environment variables, per service settings, backups and upgrading.", icon: RiSettings3Line },
  { slug: "api", title: "HTTP API", blurb: "Consumer error codes and the dashboard API with required roles.", icon: RiCodeSSlashLine },
  { slug: "providers", title: "Provider specs", blurb: "How providers are defined, validated, published and added.", icon: RiPuzzle2Line },
  { slug: "development", title: "Development", blurb: "Setup, repository layout, tests and conventions.", icon: RiToolsLine },
];

export const docBySlug = (slug: string | undefined) => DOCS.find((d) => d.slug === slug);

/** The raw Markdown URL for a doc. Served by the backend, never by the SPA. */
export const mdUrl = (slug: string) => `/docs/${slug}.md`;
