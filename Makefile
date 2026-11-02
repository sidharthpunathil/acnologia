VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all web llms build run dev test docker clean

all: build

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

llms:
	cp docs/llms.txt server/internal/llmstxt/llms.txt
	cp docs/llms-full.txt server/internal/llmstxt/llms-full.txt
	mkdir -p server/internal/docsmd/files server/internal/docsmd/assets
	cp README.md server/internal/docsmd/files/overview.md
	cp docs/architecture.md docs/flows.md docs/security.md docs/configuration.md \
		docs/api.md docs/providers.md docs/development.md server/internal/docsmd/files/
	cp docs/screenshots/*.png docs/brand/logo.png server/internal/docsmd/assets/

build: web llms
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../bin/acnologia ./cmd/acnologia

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
