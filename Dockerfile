# syntax=docker/dockerfile:1
FROM node:22-alpine AS web
WORKDIR /src/web
RUN corepack enable && corepack prepare pnpm@10.24.0 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN mkdir -p ../server/internal/webui && pnpm build

FROM golang:1.26-alpine AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/server/internal/webui/dist ./internal/webui/dist
COPY docs/llms.txt ./internal/llmstxt/llms.txt
COPY docs/llms-full.txt ./internal/llmstxt/llms-full.txt
COPY README.md ./internal/docsmd/files/overview.md
COPY docs/architecture.md docs/flows.md docs/security.md docs/configuration.md docs/api.md docs/providers.md docs/development.md ./internal/docsmd/files/
COPY docs/screenshots/*.png docs/brand/logo.png ./internal/docsmd/assets/
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /acnologia ./cmd/acnologia \
    && mkdir -p /data-empty

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /acnologia /acnologia
# distroless nonroot is uid 65532; the data dir must be writable by it.
COPY --from=server --chown=65532:65532 /data-empty /data
ENV ACNOLOGIA_ADDR=:8080 ACNOLOGIA_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/acnologia"]
