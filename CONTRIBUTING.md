# Contributing

Cairn is a small, single-owner library for pages agents write. Open a focused issue for a bug, or discuss a substantial feature before building it. Support is best effort; there is no response-time guarantee. Windows, multi-user accounts, Docker and hosted accounts are outside the supported scope.

## Development

Install the Go version listed in `go.mod` and Node 24 for browser tests. The installed application does not need Node.

```sh
go build -o cairn ./cmd/cairn
go vet ./...
go test -race ./...
python3 scripts/check-install.py ./cairn
npm ci
npx playwright install chromium
npm run test:browser
```

All five must pass before a change is ready. Browser tests are headless and use temporary data. On Linux, `npx playwright install --with-deps chromium` installs the browser's system dependencies too. Never point a test at a personal library.

Read `CONTEXT.md` for the product's words; screens, messages and documentation use them, and `internal/cairn/terms_test.go` fails when a retired word comes back. Keep page content isolated from private management, keep private access working after sharing stops, and preserve existing data on upgrade. Add focused regression tests for changes to access or storage. Run `gofmt` and keep credentials out of commits.

`internal/cairn/contract.md` holds the agent setup text below its marker. Update its checked copy at `internal/cairn/ui/agent-setup.md`, and `docs/publishing.md`, when the API changes; tests fail until they agree.

## How the interface is built

Pages are Go templates rendered on the server. There is no client framework and no build step: the stylesheet and scripts are plain files embedded in the binary.

### Stylesheets

`internal/cairn/assets.go` joins the files in `internal/cairn/ui/*.css` into one stylesheet, in the order of `stylesheetOrder`, and serves it from a content-hashed address under `/_ui/` with a year's cache. Startup fails if a file is missing from that list.

| File | Holds |
| --- | --- |
| `tokens.css` | The fonts and every token: colours per theme, type steps, spacing, sizes, radii, shadows, motion |
| `base.css` | Element defaults, buttons, fields, icons, the skip link |
| `shell.css` | The top bar, brand and library navigation |
| `forms.css` | Form pages, fields, inline errors, grouped fields, the switch, Manage |
| `library.css` | The library list, search, row menu, drawer and shortcut sheet |
| `editor.css` | The editor, its modes and the page access popover |
| `settings.css` | Settings, the theme cards, authors and the key and photo dialogs |
| `files.css` | File page downloads and per-type icons |
| `reader.css` | The reader's cover, bar, contents, sheets and end of the page |
| `feedback.css` | Notices, error pages and the password gate |
| `themes.css` | The few things a theme changes beyond its tokens |
| `focus.css` | The one focus ring, loaded last |

Rules every stylesheet follows:

- **Tokens only.** No colour, font size, radius, shadow or duration outside `tokens.css`. A theme changes tokens first; `themes.css` is for structure tokens cannot express.
- Eight type steps (`--text-meta` 12px to `--text-display` 42px), weights 400, 500 and 600, nothing under 12px. Fields use `--text-field`, which grows to 17px on touch screens.
- Spacing from `--space-1` to `--space-8`. Controls start at `--control-height` and reach `--target` (44px on touch).
- Class names are unique across files: a clash between two surfaces has broken the library before.
- No `<style>` blocks or `style=""` attributes; the policy is `style-src 'self'`.
- Field edges use `--field-line` (3:1 against their surroundings); `--line` is for decorative rules only.

The Dark block in `tokens.css` is copied under `@media (prefers-color-scheme: dark)` for System, and the dark block in `report.css` is copied for reports that follow the device. `themes_test.go` fails if a copy drifts.

### Scripts

Each page loads one script from `/_ui/`, published by `assets.go` from `pageScripts()`: the focus script (`ui/focus.js`, which every page script starts with: keyboard focus rings, browser-clock dates and the report scheme) followed by the page's own. The policy is `script-src 'self'`; there are no inline scripts.

### The shared layout

`ui/layout.html` gives every page the same head (stylesheet, colour scheme, theme colour and font preloads), skip link, top bar and `<main id="main">`. Owner pages use `{{template "owner" .}}`; anything the public listener serves uses `{{template "visitor" .}}`, which shows the brand and nothing private.

### Adding a page

1. Create `internal/cairn/ui/thing.html`:

   ```
   {{template "owner" .}}

   {{- define "title" -}}Thing · Cairn{{end}}

   {{- define "main-class" -}}form-page{{end}}

   {{- define "main" -}}
   <h1>Thing</h1>
   …
   {{- end}}

   {{- define "script" -}}{{scriptURL "forms"}}{{end}}
   ```

   Optional blocks: `topbar`, `body-class`, `after-main` (dialogs) and `script` (defaults to the focus script). A page with a script of its own adds it to `pageScripts()` in `assets.go`.
2. In Go, embed it and parse it on the layout: `//go:embed ui/thing.html` and `var thingView = pageView("thing", thingHTML, nil)`. Its data needs a `Theme` field; set it from `s.themes()`.
3. Send `default-src 'none'; ` + `uiAssetPolicy` + `; form-action 'self'; frame-ancestors 'none'; base-uri 'none'` as its policy, adding only what it needs (`connect-src 'self'` for a page that fetches).
4. Show problems with `showError` or `showVisitorError`, confirmations with a `notice`, and field errors beside their field (see `ui/feedback.html`).
5. Add a Go test for its behaviour and a check in `scripts/check-browser.cjs` for anything only a browser can see.

## Dependencies and release

Mermaid and ECharts distributions are committed under `internal/cairn/runtime`. Runtime users do not download packages. Update those files, their pinned npm versions and notices together. Regenerate third-party notices with `python3 scripts/collect-licenses.py` after `npm ci` and downloading Go dependencies.

Maintainers tag reviewed commits as `vX.Y.Z`. The release workflow reruns checks, creates Linux AMD64/ARM64 archives and checksums, and opens a draft GitHub Release. Test its downloaded archives before publishing the draft. CI uses hosted runners and no production credentials. See `scripts/release.sh` for the local packaging command.

By contributing, you agree to license your contribution under this project's MIT licence.

## README screenshots

After building `cairn` and installing the browser-check dependencies above, run:

```sh
node scripts/capture-demo.cjs
```

The script starts Cairn on localhost with a temporary library, publishes made-up example pages, and captures the library, the desktop and phone readers, the desktop reader in Dark, and the phone library in each theme, using headless Chromium. It writes the images to `docs/images/`, stops the browser and the server, then removes the demo data. It never touches your running Cairn or your own pages.
