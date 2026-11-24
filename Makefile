VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all web llms build cli run dev test docker clean

all: build

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

llms:
	cp docs/llms.txt server/internal/llmstxt/llms.txt
	cp docs/llms-full.txt server/internal/llmstxt/llms-full.txt
	mkdir -p server/internal/docsmd/files server/internal/docsmd/assets
	cp README.md server/internal/docsmd/files/overview.md
	cp docs/architecture.md docs/flows.md docs/security.md docs/configuration.md \
		docs/api.md docs/providers.md docs/cli.md docs/sdks.md docs/development.md \
		docs/deploy.md docs/deploy-railway.md docs/deploy-vps.md docs/mcp-oauth.md docs/oauth-credentials.md docs/errors.md \
		server/internal/docsmd/files/
	cp docs/screenshots/*.png docs/brand/logo.png server/internal/docsmd/assets/

build: web llms
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../bin/acnologia ./cmd/acnologia

# The acn command line client alone: standard library only, no web build.
cli:
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../bin/acn ./cmd/acn

run: build
	ACNOLOGIA_DATA_DIR=./data ./bin/acnologia

# Backend on :8080 and Vite on :5173 with hot reload.
dev: llms
	(cd server && ACNOLOGIA_DATA_DIR=../data go run ./cmd/acnologia) & (cd web && pnpm dev)

test:
	cd server && go test -race ./...
	cd sdk/go && go test ./...

docker:
	docker build -t acnologia:$(VERSION) -t acnologia:latest .

clean:
	rm -rf bin server/internal/webui/dist/assets

# Screenshot tests for the dashboard against bin/acnologia (run `make build`
# first). UPDATE=1 refreshes the baselines in e2e/__screenshots__.
.PHONY: test-screens
test-screens:
	@test -x bin/acnologia || { echo "bin/acnologia is missing, run make build first"; exit 1; }
	cd e2e && npm ci --no-audit --no-fund && npm run $(if $(UPDATE),update,test)

# The checks CI runs on a push to main, run locally before pushing: gofmt,
# go vet, golangci-lint (server/.golangci.yml), the server tests without
# -race, then the dashboard type check and unit tests. The web half needs
# web/node_modules (cd web && pnpm install). The linter and gitleaks versions
# match ci.yml; go run builds each once and caches it.
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
GITLEAKS ?= go run github.com/zricethezav/gitleaks/v8@v8.30.1
# The commits `make secrets` scans: by default the ones not on origin/main yet.
SECRETS_RANGE ?= origin/main..HEAD

.PHONY: check lint secrets
check: lint
	cd server && go test ./...
	@test -d web/node_modules || { echo "web/node_modules is missing, run: cd web && pnpm install"; exit 1; }
	cd web && pnpm typecheck && pnpm test

lint:
	@out=$$(cd server && gofmt -l .); if [ -n "$$out" ]; then printf 'Run gofmt -w on:\n%s\n' "$$out"; exit 1; fi
	cd server && go vet ./...
	cd server && $(GOLANGCI_LINT) run ./...

# The secret scan CI runs, over SECRETS_RANGE.
secrets:
	$(GITLEAKS) git --no-banner --redact --config .github/gitleaks.toml --log-opts="$(SECRETS_RANGE)" .
