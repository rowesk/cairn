# Publishing API

Agents publish pages to Cairn over a small JSON API on the private listener. They can also list, search, read, replace and archive the pages they wrote. The owner deletes pages and changes existing sharing from Cairn itself.

Everything here is on the private address only. The public listener answers `/healthz` and shared pages, and nothing under `/api`.

## Quickstart

Create an agent key in Cairn under **Settings → Authors → Connect an agent**. Then, from a machine that can open your library:

```sh
export CAIRN_URL=http://127.0.0.1:8080   # the address you open the library at
export CAIRN_API_KEY=cairn_...           # the agent key; keep it in a secret store
curl -sS -X POST "$CAIRN_URL/api/pages" \
  -H "Authorization: Bearer $CAIRN_API_KEY" -H "Content-Type: application/json" \
  -d '{"title": "Hello from my agent", "original_request": "test Cairn", "markdown": "It works."}'
```

Cairn answers `201 Created` with the page:

```json
{"id": "4f9c…", "slug": "k3T", "url": "/k3T", "private_url": "https://cairn.example.ts.net/k3T", "title": "Hello from my agent", "agent": "Research agent", "format": "markdown", "template": "report", "created_at": "2026-11-12T09:30:00Z", "updated_at": "2026-11-12T09:30:00Z", "warnings": [], …}
```

Or, with the `cairn` binary on the agent's machine, publish a file. Images the Markdown names as `?asset=NAME` are attached from beside it:

```sh
cairn publish lamps.md -request "find me a desk lamp under £100" -summary "Find a warm desk lamp under £100."
https://cairn.example.ts.net/k3T
```

`cairn help publish` lists its flags. It reads `CAIRN_URL` and `CAIRN_API_KEY`, never takes the key as a flag, prints the page's full address, prints every problem with the flag to change when Cairn refuses the page, exits 1 on failure and 2 on a mistake in the command, and retries with one `Idempotency-Key`, so a page is never saved twice. `-json` prints Cairn's reply instead. `-share` shares a new page only when the owner asked; it cannot be used with `-replace`.

The setup text in Settings tells an agent all of this in about 700 words. Copy it from the key dialog and paste it into the agent's instructions; it names `CAIRN_API_KEY` and holds no secret.

## Credentials

Send one of two credentials as `Authorization: Bearer <credential>`:

| Credential | Comes from | Byline | Reaches |
| --- | --- | --- | --- |
| Agent key (`cairn_` and 43 characters) | Settings → Authors | Its own name. `agent` in a request is ignored, with an `agent_ignored` warning | Through the API, only pages with its byline: list, search, read, replace, archive |
| Installation token | `cairn init`, in `<data>/publisher-token` | The request's `agent`, which is required | Every page |

Use an agent key for each agent, so each one signs its own pages and can be rotated or revoked alone. Cairn stores only a SHA-256 fingerprint of each key and shows a new key once. Through the API, an agent key sees and changes only pages with its byline: other authors' pages are missing from its lists and searches, and answer `404 not_found` exactly as a slug no page has. Settings will not make a key for your own default author name, because that key would reach every page you write or import. The installation token is for the owner's own scripts.

A key gives an agent its byline and keeps the API from changing other authors' pages. It does not protect the library from the agent. The library's own pages have no sign-in, so any software that can reach the private address can read every page and request and use every owner form, with or without a key. Run only agents you trust on machines that can reach the library. [SECURITY.md](../SECURITY.md#security-model) explains the model.

Browsers cannot use the API: a request with an `Origin` header or a `Sec-Fetch-Site` other than `none` gets `403 browser_origin_forbidden`.

## Endpoints

| Method | Path | Does |
| --- | --- | --- |
| GET | `/api` | A short index with one complete example. No key needed |
| GET | `/api/openapi.json` | The full OpenAPI 3.1 contract, built from the server's own types and limits. No key needed |
| POST | `/api/pages` | Publish a page, private by default |
| GET | `/api/pages` | List your pages, newest first, or search them with `q` |
| GET, HEAD | `/api/pages/{slug}` | Read a page with its request and the Markdown or HTML Cairn kept. HEAD checks that it exists and your key reaches it |
| PUT | `/api/pages/{slug}` | Replace a page completely |
| PATCH | `/api/pages/{slug}` | Archive or restore a page, or change its title |
| GET | `/healthz` | Whether Cairn is up. No key needed. Also on the public listener |

There is no DELETE. Deleting is permanent and, like sharing, stays with the owner: `DELETE` answers `405 method_not_allowed` and says to archive with `PATCH` instead. Archived pages leave the library list, keep their links and can be restored.

## Publication fields

`POST /api/pages` and `PUT /api/pages/{slug}` take one JSON object. Send exactly one of `markdown` or `html`. Sizes are in bytes of UTF-8.

| Field | Type | Required | Limit | Notes |
| --- | --- | --- | --- | --- |
| `title` | string | Yes | 300 bytes | Says what the page covers or concludes |
| `original_request` | string | Yes | 32 KiB | The owner's own words. Private: never on a shared page |
| `agent` | string | With the installation token | 100 bytes | The byline. An agent key sets its own |
| `request_summary` | string | No | 40 words and 1000 bytes | The request from the owner's side, shown in the library |
| `request_source` | string | No | 80 bytes, one line | The app, such as Claude Code. Private |
| `markdown` | string | One of the two | 2 MiB, and 2 MiB rendered | Cairn keeps the Markdown, so the page can be read back and edited in Cairn |
| `html` | string | One of the two | 2 MiB | A complete document with its own layout. Runs in a sandbox with no network access |
| `template` | string | No | `report`, `comparison` or `visual` | Markdown only; default `report`. Ignored with `html`, with a `template_ignored` warning |
| `slug` | string | No | 1 to 64 of `A-Z a-z 0-9 _ -`, starting with a letter or digit | Leave it out and Cairn picks a short free one. Cairn's own route names are reserved |
| `capabilities` | array of strings | No | `mermaid`, `echarts` | For `html`: bundled libraries to load before the page's scripts. Markdown loads them itself |
| `assets` | object | No | 32 images, 4 MiB each | File name to base64 bytes. PNG, JPEG, GIF or WebP. Padding, either base64 alphabet, line breaks and `data:` URLs are all accepted |

Name images with letters, digits, hyphens or underscores, up to 96 characters, then `.png`, `.jpg`, `.jpeg`, `.gif` or `.webp`, and refer to them as `?asset=name.png`. That address works before the page's slug is known. [docs/authoring.md](authoring.md#markdown) covers what the Markdown can hold: footnotes, callouts, images, Mermaid, ECharts and the raw HTML Cairn keeps.

Fields Cairn does not take are refused, never ignored. A likely typo says so (`titel is not a field Cairn accepts. Did you mean title?`), and fields for passwords or existing sharing stay with the owner.

New slugs must differ from every existing slug and public link name by more than letter case, so `lamp-report` and `Lamp-Report` never name two pages. Pages saved before this rule keep their addresses.

## Responses

A create answers `201` with a `Location` header; a replace or update answers `200`. All three return the page:

| Field | Meaning |
| --- | --- |
| `slug`, `url` | The page's address and its path, such as `/k3T` |
| `private_url` | The full private address, built from the server's `CAIRN_PRIVATE_BASE_URL` (`-private-base-url`). `null` when that is not set, with a `private_url_unset` warning: join `url` to the address you sent the request to. Cairn never builds it from the request's `Host` |
| `public_url` | The public address after requested shared creation, omitted on private creation and replacement |
| `share_enabled` | Optional creation-only boolean; never send on PUT |
| `format`, `template` | `markdown`, `html` or `file`; the Markdown template or `null` |
| `title`, `agent`, `original_request`, `request_summary`, `request_source` | As saved |
| `assets` | Image names, each served at `/_assets/{slug}/{name}` |
| `shared`, `archived`, `editable` | State. `editable` is true when Cairn kept Markdown its editor can open |
| `created_at`, `updated_at` | RFC 3339, UTC. `updated_at` changes when the content, title or archive state does |
| `warnings` | What Cairn changed or could not do. Empty when nothing |
| `id` | Cairn's internal identifier. Routes use `slug` |

Warnings never stop a save:

| Code | Means |
| --- | --- |
| `dropped_html` | Raw HTML in the Markdown that Cairn does not allow was removed. `tags` and `attributes` list what; `href` means an unsafe address. Tell the owner |
| `agent_ignored` | An agent key sent an `agent` other than its own name |
| `template_ignored` | `template` was sent with `html` |
| `private_url_unset` | The server has no private base URL, so `private_url` is `null` |
| `storage_cleanup_pending` | Saved, but an old version's files could not be removed yet. Cairn retries before the next save. The old `warning` string carries the same news |

## Listing and searching

`GET /api/pages` returns `{"pages": […], "total": 12, "has_more": true, "next_cursor": "…"}`, newest first.

| Parameter | Does |
| --- | --- |
| `q` | Search titles, requests, bylines and the words of each page, as the library's search does: every word must match, each as the start of a word, `"quoted words"` as a phrase, ignoring case and accents. Results come best match first, and each has `match`: where the words were (`title`, `request`, `agent` or `page`), the matching text, and `highlights` as `[start, end)` character spans |
| `agent` | Only pages with this byline. An agent key may name only itself; it lists only its own pages either way |
| `archived` | `true` lists the archive instead |
| `limit` | 1 to 100, default 20 |
| `cursor` | `next_cursor` from the previous response |

List items carry `slug`, `url`, `private_url`, `title`, `agent`, `request_summary`, `excerpt` (the page's first real paragraph), `shared`, `archived`, `editable`, `created_at` and `updated_at`, but never the original request. Unknown parameters are refused with a suggestion (`search` becomes `q`).

## Reading a page

`GET /api/pages/{slug}` returns the page and `source`, what Cairn kept of how it was written:

- Markdown sent through the API, written in the editor or imported from a `.md` file: `{"format": "markdown", "name": "page.md", "text": "…"}`.
- HTML sent through the API: `{"format": "html", "text": "…"}`, the stored document without the runtime tags Cairn adds, so it can be sent back unchanged.
- Imported PDFs and other files: `{"format": "file", "name": "notes.pdf", "text": null, "download_url": "/k3T?download=1"}`.
- Markdown sent through the API before Cairn kept a copy: `{"format": "markdown", "text": null}`.

A page with another byline answers an agent key with `404 not_found`, the same status and body as a slug no page has.

## Changing a page

`PUT /api/pages/{slug}` replaces a page completely: send the whole publication again, with every image it still needs. The slug, creation date and sharing stay; a shared page shows the new content to its readers at once. `slug` may be left out or must match. Leaving `request_summary` or `request_source` out keeps the saved ones. A failed replacement leaves the page as it was. There is no version history, so replace a page only when the owner asks.

`PATCH /api/pages/{slug}` takes `archived` (true or false), `title`, or both.

An agent key changes only pages with its byline; another author's page answers `404 not_found`, as if it did not exist.

## Retries and idempotency

Send `Idempotency-Key: <any unique string>` on a POST, PUT or PATCH, such as a UUID. If the same credential sends the same request with the same key within 24 hours, Cairn returns the first result with `Idempotent-Replayed: true` and saves nothing again. The same key with a different body answers `422 idempotency_key_reused`; a second request while the first still runs answers `409 idempotency_key_in_progress`, which is retryable. Only successful results are kept, so a corrected request can reuse its key. Keys belong to one credential, so two agents can never collide.

Cairn keeps only which page the first request wrote, its status and its address, never the page's title, Markdown or request. A replay answers with the page as it is now, so it shows any later change, and its `warnings` are today's, not the first request's. Replies go after 24 hours, when the page is deleted, and when the key that made them is rotated or revoked; if the page has gone, the request runs again.

Without a key, two identical POSTs make two pages.

## Errors

Every failure is JSON with one shape. Branch on `code`, never on `message`.

```json
{
  "code": "validation_failed",
  "message": "title is required. Give the page a short, specific title.",
  "field": "title",
  "retryable": false,
  "errors": [
    {"code": "required", "field": "title", "message": "title is required. Give the page a short, specific title."},
    {"code": "required", "field": "original_request", "message": "original_request is required. Send the owner's request in their own words."},
    {"code": "invalid_value", "field": "template", "message": "template \"fancy\" is not one of report, comparison or visual.", "actual": "fancy", "allowed": ["report", "comparison", "visual"]}
  ]
}
```

`errors` lists every problem Cairn found, in field order, so one reply is enough to fix a request. The top-level `message` and `field` repeat the first problem, as before. Each problem can carry `limit` and `unit` (`bytes`, `words` or `items`), `actual` (the size sent, or the value when it is short), `allowed`, and for charts or broken JSON, `line`, `column` and `block`.

| HTTP | `code` | What to do |
| --- | --- | --- |
| 400 | `validation_failed` | Fix each entry in `errors` |
| 400 | `invalid_json` | The body is not one JSON object. `message`, `line` and `column` say where |
| 401 | `authentication_required` | No `Authorization: Bearer` header, or a malformed one |
| 401 | `invalid_credential` | The key is wrong, rotated or revoked. Ask the owner for a new one |
| 403 | `browser_origin_forbidden` | Send the request from a server-side client |
| 403 | `forbidden` | An agent key asked for another author's list (`agent=`) |
| 404 | `not_found` | No page your credential reaches has that slug, or no route has that path |
| 405 | `method_not_allowed` | Includes DELETE; archive with PATCH instead |
| 409 | `slug_conflict` | The slug is taken, letter case aside. Leave `slug` out |
| 409 | `public_slug_unavailable` | Owner sharing only |
| 409 | `idempotency_key_in_progress` | Wait and send again with the same key. Retryable |
| 413 | `payload_too_large` | The request is over 16 MiB. Send smaller images |
| 415 | `unsupported_media_type` | Send `Content-Type: application/json` |
| 422 | `idempotency_key_reused` | Use a new key for a new request |
| 500 | `storage_unavailable`, `link_allocation_failed` | Retryable. Send again with the same `Idempotency-Key` |
| 503 | `storage_cleanup_pending`, `public_sharing_unavailable` | Retryable |

Codes inside `errors`: `required`, `too_long`, `too_many_words`, `multiple_lines`, `conflict`, `invalid_value`, `unknown_field`, `wrong_type`, `invalid_base64`, `invalid_slug`, `reserved_slug`, `invalid_asset_name`, `empty_asset`, `too_large`, `too_many_items`, `unsupported_image`, `invalid_chart`, `syntax`, `read_only`, `not_allowed` and `slug_taken`.

Until 1.1, a wrong key answered `authentication_required` and unknown fields answered `invalid_json`; both have their own codes now, with the same HTTP status.

## Limits

| What | Limit |
| --- | --- |
| Whole request | 16 MiB, base64 included |
| `markdown`, `html`, rendered Markdown | 2 MiB each |
| Images | 32 per page, 4 MiB each |
| `title` | 300 bytes |
| `original_request` | 32 KiB |
| `request_summary` | 40 words and 1000 bytes |
| `request_source` | 80 bytes, one line |
| `agent` | 100 bytes |
| `slug` | 64 characters |
| `q` | 200 bytes, 12 words |
| `limit` | 100 |
| Idempotency keys | 255 visible ASCII characters, kept 24 hours |

`GET /api` serves the same numbers as JSON, along with `slug_chars`, the length of the addresses Cairn picks, which the owner sets in Settings.

## Health and logs

`GET /healthz` answers `{"status": "ok"}` on the public listener, for a proxy or monitor in front of it. On the private listener it adds the version and whether the database answers and the data directory is writable: `{"status": "ok", "version": "v0.3.0", "database": "ok", "storage": "ok"}`. Either answers `503` with `"status": "unavailable"` when something is wrong.

Cairn writes one line to standard error (the journal, under systemd) for each publish, replace and update, each refused credential and each server error:

```
time=2026-11-12T09:30:00Z level=INFO msg=publish listener=private method=POST path=/api/pages status=201 ms=14 slug=k3T agent="Research agent" credential=agent_key format=markdown warnings=0
time=2026-11-12T09:31:02Z level=WARN msg="auth failure" listener=private method=GET path=/api/pages status=401 ms=0 remote=100.101.102.103:52144 reason=invalid
```

Lines never hold keys, tokens, request bodies, titles or original requests.

## Reading routes

These serve pages to people, not agents:

- `GET /{slug}` is the owner's reader, with the byline and private request.
- `GET /_content/{slug}` is the page alone, in its sandbox, without private metadata.
- `GET /_content/{slug}?asset=name.png` and `GET /_assets/{slug}/name.png` serve the page's images.
- `GET /{slug}?download=1` downloads the kept source file where there is one.

Sharing is in [docs/sharing.md](sharing.md). The API can share a new page only when the owner asked, and a shared page keeps its private request private.

## Sharing a new page

Publish privately by default. Set `share_enabled` to `true` on `POST /api/pages` only when the owner asked for this page to be shared. Never set it otherwise. Tell the owner the saved `public_url` afterwards, as well as the private address. Sharing must be configured or creation fails with 503 `public_sharing_unavailable` without saving. Omitted or `false` stays private. Any supplied value on PUT is refused; replacement preserves sharing, link names and passwords. Agent-created files start private.

## Files

`POST /api/files` publishes a file page from `multipart/form-data`, with the same `Authorization` header and browser-origin refusal. A file page shows a filetype icon, the title, filename, type, size and a **Download** button in place of page HTML. The Download action returns originals as `application/octet-stream` attachments.

- `file`: exactly one file part, at most 512 MiB. Any type is accepted. The filename must be plain UTF-8 of at most 255 bytes.
- `original_request`: required, as for pages.
- `title`, `request_summary`, `request_source`, `slug`: optional, with the same rules as pages. A blank title uses the filename without its extension.
- `agent`: only for the installation token, as for pages.
- `share_enabled`: optional multipart text `true` or `false`, creation only. Omitted or `false` stays private. Use `true` only when the owner asks; sharing must be configured. Any supplied value on replacement is refused.

Any other field or file part is rejected with `validation_failed`. The whole request may be at most 530 MiB, including fields and multipart overhead. Cairn streams the file to a temporary file inside the data directory, never into memory or `/tmp`, and renames it into the page's object once it is complete. Interrupted uploads are removed at startup.

```sh
curl --fail-with-body --silent --show-error \
  -X POST "http://127.0.0.1:8080/api/files" \
  -H "Authorization: Bearer $CAIRN_PUBLISHER_TOKEN" \
  -F "file=@quarterly-figures.xlsx" \
  -F "agent=Research assistant" \
  -F "original_request=Keep the quarterly figures spreadsheet"
```

Success and errors match `POST /api/pages`. A file over 512 MiB returns 413 `payload_too_large`. `PUT /api/files/<slug>` replaces any page with a file page, and `PUT /api/pages/<slug>` turns a file page back into a page. Downloads release Cairn's storage lock before sending, so a slow 512 MiB download does not hold up saves.

## Short links

`POST /api/links` accepts JSON with the usual `Authorization: Bearer` header. Browser-origin requests are refused. The public listener has no API routes.

```json
{"url":"https://example.org/reading","slug":"reading","share_enabled":true}
```

`url` is required and must be an HTTP or HTTPS URL of at most 2,048 characters, with a valid host and no credentials. Cairn refuses its own public link paths to prevent redirect loops. `slug` is optional; omit it to let Cairn choose a link name. `share_enabled` defaults to false. Set it to true only when the owner asks. Shared creation without a public origin fails with 503 without saving.

Success returns 201 with the usual page-write fields, including `slug`, `url` and `private_url`, plus `public_url` for shared creation. Read the item through `GET /api/pages/{slug}` for `format: "link"`, `target_url` and `clicks`. The list endpoint includes pages, files and links. Links accept no password or original request fields. The owner edits targets and sharing in Manage; page and file replacement cannot overwrite a link.

Public short addresses return an empty 302 response. GET visits count asynchronously; HEAD does not. An unshared link returns 404 publicly. Archiving keeps redirects working.
