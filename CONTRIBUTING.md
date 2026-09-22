# Contributing

Cairn is a small, single-owner report library. Open a focused issue for a bug or discuss a substantial feature before building it. Support is best effort; there is no response-time guarantee. Windows, multi-user accounts, Docker and hosted accounts are outside the initial release's supported scope.

## Development

Install the Go version listed in `go.mod` and Node 24 for browser tests. The installed application does not need Node.

```sh
go test -race ./...
go vet ./...
go build -o cairn ./cmd/cairn
npm ci
npx playwright install chromium
npm run test:browser
python3 scripts/check-install.py ./cairn
```

Browser tests are headless and use temporary data. On Linux, `npx playwright install --with-deps chromium` installs browser system dependencies too. Do not use a personal library for tests.

Read `CONTEXT.md` for the product vocabulary. Keep report content isolated from private management, retain private access after revocation, and preserve existing data on upgrade. Add focused regression tests for changes to access or storage behaviour. Run gofmt and keep credentials out of commits.

`internal/cairn/contract.md` supplies the Settings agent guide. Update its checked copy at `internal/cairn/ui/agent-setup.md` and the operator documentation when the API changes.

## Dependencies and release

Mermaid and ECharts distributions are committed under `internal/cairn/runtime`. Runtime users do not download packages. Update those files, their pinned npm versions and notices together. Regenerate third-party notices with `python3 scripts/collect-licenses.py` after `npm ci` and downloading Go dependencies.

Maintainers tag reviewed commits as `vX.Y.Z`. The release workflow reruns checks, creates Linux AMD64/ARM64 archives and checksums, and opens a draft GitHub Release. Test its downloaded archives before publishing the draft. CI uses hosted runners and no production credentials. See `scripts/release.sh` for the local packaging command.

By contributing, you agree to license your contribution under this project's MIT licence.
