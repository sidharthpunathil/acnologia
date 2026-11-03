# Contributing to Acnologia

Thanks for helping. Bug reports, fixes, docs, new provider specs and SDK
improvements are all welcome.

## Before you start

- **Found a security issue?** Do not open a public issue. See
  [SECURITY.md](SECURITY.md).
- **Planning something large?** Open an issue first so we can agree on the
  approach before you spend time on it.
- **Small fix?** Send the pull request directly.

## Set up

You need Go 1.26, Node 22 and pnpm 10.

```sh
git clone https://github.com/sidharthpunathil/acnologia && cd acnologia
make dev     # Go on :8080 and Vite on :5173 with hot reload
make test    # server tests with the race detector, plus the Go SDK
make build   # bin/acnologia with the dashboard embedded
```

On Windows use `.\scripts\build.ps1` instead of make. The
[development guide](docs/development.md) covers the repository layout, the
test suites, the screenshot tests and building on every platform.

## Making a change

1. Branch from `main`.
2. Add or update tests. Server changes need a Go test; dashboard changes that
   alter what a page shows need the screenshot baselines refreshed
   (`make build && make test-screens UPDATE=1`, then look at the images).
3. Run the checks:

   ```sh
   cd server && gofmt -l . && go vet ./... && go test ./...
   cd web && npx tsc --noEmit
   ```

4. Update the docs in `docs/` when behaviour, routes or settings change, then
   run `make llms` so the copies embedded in the binary match.
5. Open a pull request that says what changed and why, and how you tested it.

## Style

- Go: `gofmt`, the standard library first, small packages with tests next to
  the code.
- Dashboard: follow [web/AGENTS.md](web/AGENTS.md). Use the BoardUI
  components and semantic color tokens, never raw palette classes.
- Writing (docs, UI text, commit messages): plain, short sentences. No em
  dashes.
- Commit messages: an imperative subject line ("Add key scopes to shares"),
  then a short body explaining why.

## Adding a provider

Providers are data. A new provider is a spec: base URL, where the key goes,
how to classify the provider's errors, a health check and optional offers.
See [docs/providers.md](docs/providers.md) for the format and the fixture
tests every built in spec needs.

## Code of conduct

Everyone taking part is expected to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).
