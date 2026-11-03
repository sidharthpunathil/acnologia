// Railway Infrastructure as Code for Acnologia: one service built from the
// Dockerfile, one replica, a volume at /data. It replaces railway.json, which
// Railway stops reading on 2026-12-01.
//
// Railway does not read this file on deploy; the CLI applies it:
//
//   railway login && railway link        # or `railway init` for a new project
//   railway variables --set "ACNOLOGIA_ACCESS_PASSWORD=$(openssl rand -base64 24)" \
//     --set "ACNOLOGIA_MASTER_KEY=$(openssl rand -base64 32)"
//   railway config plan                  # read it: nothing unexpected deleted
//   railway config apply
//
// Deploying from a fork? Change the repository below. The secrets use
// preserve(), so the file never holds them and applying it keeps what you
// set. See docs/deploy-railway.md.
import { defineRailway, github, preserve, project, service, volume } from "railway/iac";

export default defineRailway(() => {
  const data = volume("acnologia-data", { sizeMB: 1024 });

  const acnologia = service("acnologia", {
    source: github("sidharthpunathil/acnologia", { branch: "main" }),
    // SQLite is one file on one volume: never more than one replica.
    replicas: 1,
    healthcheck: "/healthz",
    healthcheckTimeout: 120,
    volumeMounts: { "/data": data },
    env: {
      // New volumes are owned by root and the image runs as a nonroot user.
      RAILWAY_RUN_UID: "0",
      ACNOLOGIA_DATA_DIR: "/data",
      ACNOLOGIA_REQUIRE_ACCESS_PASSWORD: "true",
      ACNOLOGIA_SECURE_COOKIES: "true",
      ACNOLOGIA_TRUST_PROXY_HEADERS: "true",
      ACNOLOGIA_ACCESS_PASSWORD: preserve(),
      ACNOLOGIA_MASTER_KEY: preserve(),
      ACNOLOGIA_PUBLIC_URL: preserve(),
      ANTHROPIC_API_KEY: preserve(),
    },
  });

  return project("acnologia", { resources: [acnologia, data] });
});
