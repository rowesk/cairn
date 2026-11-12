# Changelog

## Unreleased

- Keep streamed file pages up to 50 MiB and explicit, creation-only agent sharing alongside the new reader and publishing API.
- Require Go 1.26.11.

### Reading

- The reader is rebuilt around the navy Blue Hour cover: brand, status (Private or Shared), Copy link, Edit, Send to Kindle and Manage page in one row, then the title, the byline and your request, clamped to three lines with Show full request. Phones get a More menu. Visitors see a plain strip with the title, byline and Copy link.
- The compact bar appears once the cover scrolls away and stays for the rest of the read, with the current section, a progress line, Copy link and More. Page details open from its title.
- Contents sit in their own column beside the text on wide screens, lined up with the cover, and step aside while a wide table, figure or diagram passes. Elsewhere they open as a sheet. C, [, ] and ? work from anywhere on the page, inside it too.
- The end of a page offers the previous and next page, following the search, author or order you opened it from, plus Archive, Share or Copy link, Edit and Back to the library.
- Markdown pages gain footnotes, callouts (including `[!VERDICT]` and `[!SUMMARY]`), figures with captions, labelled code blocks, curly quotes and a safe set of raw HTML. An opening heading that repeats the title no longer shows twice.
- Tables, diagrams and charts fit phones: tables scroll with a pinned first column and header row, diagrams scroll sideways rather than shrink below 12px, and charts keep 12px labels and reflow on rotation. Broken diagrams show their source.

### Finding pages

- Search covers every word of every page, as you type, with matches highlighted and the matching lines of a request or page shown under the title. Accents and letter case are ignored and words match from their start.
- A dot marks pages you have not opened. Rows show the request summary and a cleaner excerpt.
- The library works from the keyboard: `/` or Command or Control K to search, J and K to move, Space to preview, E to archive with undo, ? for the list.
- The list shows 50 pages and loads more in place. An empty library offers three ways in: connect an agent, write a page, import a file.
- Dates read as "Just now", "Yesterday" or "4 Nov" in your own browser's time zone, with the full date and 24-hour time on hover.

### Writing and managing

- Pages written in Cairn or imported as Markdown can be edited at `/edit/<link name>`. The editor has Write, Preview and Side by side (from 1200px wide), keeps a draft on the device until you save, and checks the link name while you type.
- Import is one drop zone with optional details, each with its own help and error.
- Manage page has a Sharing switch, the public link with Copy link, the link name and an optional password in one place. Archive and restore stay on the page; Delete asks once, in place.
- Errors appear beside the field they concern and keep what you typed. A form left open across a restart still saves. Error pages, for you and for visitors, look like Cairn and say what to do next.
- Passwords for shared pages need at least 8 characters.

### Themes

- Dark: Tide's blues on deep slate, covering every screen, with Markdown pages, their charts and their diagrams turning dark too.
- System follows each device's light or dark setting, visitors' devices on shared pages included, and is the default for new installations. Existing installations keep the theme they saved.
- Minimal is redesigned as black type on white with thin rules and square corners, and still downloads no font. Futuristic now colours Markdown pages in its own indigo and violet, so the frame and the page match.
- Settings → Appearance applies a theme in one click on its card, with a preview drawn in the theme's own colours. **Customise per view** gives the reader or shared pages a theme of their own. Without JavaScript the cards are radio buttons with a Save button.
- Pages in a design of their own keep it; in a dark theme they sit on the page as a sheet of paper.

### Agents and the API

- Settings leads with **Authors**: connecting an agent is one name and **Create key**. The key dialog shows the key once, then the setup text or a `cairn publish` command, then a test. Rotate and revoke confirm inside the dialog. Each row says whether its key is active and when it was last used.
- The setup text agents receive is rewritten for them, about 700 words, with no key and no maintainer notes in it.
- Agents can list and search their pages (`GET /api/pages?q=`), read one back with its Markdown or HTML (`GET /api/pages/{slug}`, and `HEAD`), and archive or retitle it (`PATCH`). Through the API, an agent key reaches only pages with its own byline, and another author's page answers `404` exactly like a missing one. There is no `DELETE`: deleting, like sharing, stays with the owner.
- Markdown sent through the API is kept with the page, so it can be read back and edited in Cairn.
- Every validation failure names its field with the limit and the value sent, and one reply lists every problem in `errors`. Unknown fields, wrong types and bad base64 are now `validation_failed` (they were `invalid_json`), and a wrong, rotated or revoked key is `401 invalid_credential`. Charts with invalid JSON are refused with their block and line.
- Publishing answers with the whole page, including `updated_at` and `warnings` such as the raw HTML Markdown dropped. `private_url` comes from the new `-private-base-url` / `CAIRN_PRIVATE_BASE_URL` setting.
- `Idempotency-Key` on POST, PUT and PATCH replays the first result for 24 hours, rebuilt from the page as it is now. Cairn stores only which page a request wrote, never its content, and drops replies when they expire, when the page is deleted and when the key is rotated or revoked.
- New link names must differ from existing ones by more than letter case. Existing pages keep their addresses.
- `GET /api/openapi.json` is an OpenAPI 3.1 contract built from the server's own types; `GET /api` is a short index with one complete example. API version 1.1.
- `cairn publish FILE.md` publishes a Markdown or HTML file with its images and prints the page's address, using `CAIRN_URL` and `CAIRN_API_KEY`.

### Send to Kindle

- Send any page to your Kindle as an EPUB from the library's row menu or the reader. Cairn sends it through your SMTP account to the Kindle address saved in Settings. See [docs/kindle.md](docs/kindle.md).
- A failed send says why: the mail server could not be reached, refused the sign-in, refused the message or asked to try later. Try again shows only when trying again can work.

### Running Cairn

- `cairn help` describes every command and flag. `cairn serve` names the default action, and unknown commands, flags and stray arguments are refused with a hint instead of starting the server.
- Every flag has a `CAIRN_*` environment variable, such as `CAIRN_LISTEN` for `-listen`; a flag wins over its variable. `deploy/cairn.service` no longer passes `-listen`, `-public-listen` or `-public-base-url`, because Cairn reads them from `/etc/cairn/cairn.env` itself. Existing service files keep working.
- `serve` finds the installation token in `<data>/publisher-token` on its own, so `cairn serve -data ./data` is enough after `cairn init`. Messages call it the installation token; the file, flag and variable names are unchanged.
- Startup prints the version, data directory, sharing and Send to Kindle state and the address to open. Errors name the setting involved and say what to do.
- `GET /healthz` on both listeners. One log line per publish, replace, update, refused key and server error, without keys, bodies, titles or requests.

### Faster and easier to use

- Pages are about a tenth of their old size. The stylesheet, fonts and page scripts are served from content-hashed addresses, cached for a year and compressed, so an empty library is under 5 KB and a warm page load fetches only its HTML. Mermaid and ECharts are sent compressed, and load after the page when it has no scripts of its own.
- Eight text sizes from 12px up, three weights, a consistent set of corners and shadows, and every text pair at 4.5:1 or better in every theme.
- Every control is 44px on touch screens, form fields are large enough that iOS does not zoom into them, and keyboard focus always shows.

### Security

- Each listener answers only the names its owner reaches it by, so a website cannot reach the library by pointing its own name at your machine (DNS rebinding). The private listener takes IP addresses, `localhost`, single-label tailnet names, names under `.ts.net`, the private base URL and any names in the new `-allowed-hosts` / `CAIRN_ALLOWED_HOSTS`. The public listener takes its public base URL's name and loopback. Anything else gets `421` and a log line. See [Troubleshooting](docs/troubleshooting.md#access).
- Docs, the API index and the key dialog now say what a key does: it gives an agent its byline and keeps the API from changing other authors' pages. It does not protect the library from software that can reach the private address. SECURITY.md has a new Security model section, and [docs/owner-login-proposal.md](docs/owner-login-proposal.md) weighs an optional owner sign-in.
- The owner form token is masked afresh every time a page shows it, so compressed pages that echo a search cannot leak it.
- A wrong share password slows further tries from the same address, doubling up to 5 minutes, while other visitors wait at most 10 seconds. Checking a password no longer holds up the rest of the library. Manage page suggests a new password when Cairn never checked the old one's length.
- Settings will not make an agent key for your own default author name, which would reach every page you write or import, and will not set the default author to a name that has a key.
- Pages count as read only when you open them, not when another website makes the browser fetch them.
- Editor drafts of saved pages are kept by page, not by address, last 30 days, and go when you delete the page in that browser.
- `/_runtime/` serves its three scripts and nothing else; there is no folder listing. Unknown addresses under `/_runtime/` and `/_ui/` get the themed not-found page.
- Schema changes add each column and its values in one transaction, and the search index puts right pages that an older version replaced, renamed or deleted.

### Changed behaviour

- Shared pages no longer offer Download original, and the public listener refuses `?download=1`. The owner still downloads the source from the private reader.
- Renaming, archiving and restoring a page in Cairn now update its `updated_at`.
- Requests whose Host header is not an allowed name now get `421 Misdirected Request`. If you open Cairn at your own domain name, add it to `CAIRN_ALLOWED_HOSTS` or set `CAIRN_PRIVATE_BASE_URL` before you upgrade.
- An agent key reading, replacing or archiving another author's page gets `404 not_found` (it was `403 forbidden`), and a slug conflict no longer names the page that holds the slug.
- The library's deletion notice comes from a short-lived record on the server, not from the address, so an old link says "Page deleted.".
- One wrong password at a shared page's gate no longer makes the next try wait; the pause starts at the second wrong password in a row, and a try made during it is held and sent when it ends.
- A Kindle address whose domain has no dot, such as `name@kindle`, is refused rather than saved.
- File cards and their library rows name the kind briefly ("PDF · 47 KB"), and a shared file card offers no Download to visitors. The reader offers an imported PDF, Word, OpenDocument or RTF file once, from its card.
- Pages rendered from now on keep a number with its unit ("6.1 kW", "2 × 8 TB") on one line.

## v0.2.0

- Saved preset themes (Cairn, Minimal, Futuristic) for the admin view, private reader and public reader.
- One page scroll in the reader at every width: a shorter cover band, a pinned bar with the title, current section and page actions, and a page-details sheet with the byline and full request.
- Nested contents list from report headings, set in the report's left margin beside its text when there is room and opened from the bar otherwise, with current-section tracking and `#section` deep links.
- Headings without ids get stable GitHub-style ids when served, so Markdown section links work.
- Bundles Mermaid 12.2.0, which ships DOMPurify 3.4.17 and KaTeX 0.18.10 and clears the critical advisories npm reported against Mermaid 11.17.2.
- Updates golang.org/x/net to v0.62.0, which fixes two HTML parser advisories (GO-2026-6866, GO-2026-6860) in code paths Cairn uses, along with golang.org/x/crypto v0.58.0 and golang.org/x/sys v0.49.0.
- Builds with Go 1.26.10, which fixes standard-library vulnerabilities in `html/template`, `net/http` and `crypto/x509` reported by govulncheck.
- Fixed in-report section links that did nothing on phones, and runaway frame heights for HTML reports without a doctype.

## v0.1.0

Initial public release under the MIT licence.

- Private searchable library, report previews and responsive readers.
- Markdown editor, file imports, original downloads and managed images.
- Sandboxed HTML, packaged Mermaid diagrams and ECharts charts.
- Per-agent publishing keys, author profiles and private request summaries.
- Optional public sharing and passwords, with matching private/public paths.
- Three-character mixed-case alphanumeric paths by default.
- Idempotent owner/credential setup and Linux AMD64/ARM64 release archives.

Existing personal installations retain their data, credentials, preferences and URLs. This release introduces no incompatible database migration.
