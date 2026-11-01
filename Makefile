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
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../bin/cogito ./cmd/cogito

run: build
	COGITO_DATA_DIR=./data ./bin/cogito

# Backend on :8080 and Vite on :5173 with hot reload.
dev: llms
	(cd server && COGITO_DATA_DIR=../data go run ./cmd/cogito) & (cd web && pnpm dev)

test:
	cd server && go test -race ./...
	cd sdk/go && go test ./...

docker:
	docker build -t cogito:$(VERSION) -t cogito:latest .

clean:
	rm -rf bin server/internal/webui/dist/assets
