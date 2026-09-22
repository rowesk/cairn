# Publishing a private page

## Request

Send JSON to `POST /api/pages` with `Authorization: Bearer <publisher token>` and `Content-Type: application/json`. This is an agent/server interface. Browser-origin publication requests are refused. Reading requires no token within the private network boundary.

Required metadata fields are `title` and `original_request`. Supply exactly one of `html` or `markdown`. A per-agent key supplies its configured author byline; omit `agent`, since any supplied value is overridden. The legacy installation token still requires an explicit `agent` byline. Optional fields are `request_summary`, `request_source`, `slug`, `assets`, `template` and `capabilities`. Unknown fields are rejected, including sharing settings.

- `title`: nonblank, at most 300 UTF-8 bytes.
- `agent`: nonblank, at most 100 bytes when using the installation token. Per-agent keys bind the byline and allow replacement only of pages with that exact author name.
- `original_request`: the owner's words, nonblank, at most 32 KiB. It is private metadata, not report HTML.
- `request_summary`: agents should supply the request from the owner's perspective in at most 40 words and 1000 bytes. The server accepts omission for older integrations. This appears only in the private reader and library.
- `request_source`: optional known app label, at most 80 bytes and one line. Never guess it. It stays private.
- `html`: nonblank, at most 2 MiB. Custom HTML is stored once.
- `markdown`: nonblank, at most 2 MiB; converted once into saved HTML at publication.
- `template`: `report`, `comparison` or `visual`; Markdown defaults to `report`. Custom HTML keeps its own layout.
- `capabilities`: optional array containing `mermaid` and/or `echarts`. Cairn inserts locally served libraries before custom scripts. Markdown fences named `mermaid` or `echarts` load their library automatically; ECharts fences contain JSON options.
- `slug`: 1 to 64 ASCII letters, digits, underscores or hyphens, beginning with a letter or digit. Application route names are reserved. Omit it for a generated route.
- `assets`: an object mapping simple image filenames to base64-encoded image bytes. Up to 32 images, each at most 4 MiB. PNG, JPEG, GIF and WebP only. Filenames use letters, digits, underscores or hyphens followed by a supported image extension. The entire JSON request is limited to 16 MiB.

Reference a supplied image with `src="?asset=bulb.png"` in the report HTML. This resolves to that page's own assets and works before a generated slug is known. The explicit `/_assets/<slug>/bulb.png` route also works once the slug is known. No external image downloading is performed by Cairn.

Use `PUT /api/pages/<slug>` with the same complete publication body to explicitly replace a page and its assets. The slug may be omitted or must match the existing route. The original creation date, identity and route remain stable. Assets omitted from a replacement are removed. Omitted or empty `request_summary` and `request_source` retain the saved values. A failed replacement leaves the last successful page intact.

## Example

Create a JSON file such as:

```json
{
  "title": "Bulb comparison",
  "agent": "Example agent",
  "original_request": "Compare bulbs for my bedroom",
  "slug": "report",
  "markdown": "# Bulb comparison\n\nThe report goes here.",
  "template": "report"
}
```

Send it with a token loaded from the private environment, without printing the token:

```sh
python3 - <<'PY'
import json, os, urllib.request
with open('publication.json', 'rb') as source:
    payload = source.read()
request = urllib.request.Request(
    'http://127.0.0.1:8080/api/pages',
    data=payload,
    headers={
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + os.environ['CAIRN_PUBLISHER_TOKEN'],
    },
)
with urllib.request.urlopen(request) as response:
    print(json.load(response))
PY
```

Creation returns HTTP 201 with `id` and `url`, such as `/report`, and the same path in `Location`. Replacement returns HTTP 200. Append `url` to your configured private base address; Cairn does not derive an external address from an untrusted Host header.

Every `/api` response uses `Content-Type: application/json`. Failures return one shape, `{"code","message","field?","retryable"}`, with stable codes for machine branching:

| HTTP | Code | Meaning |
| --- | --- | --- |
| 400 | `validation_failed` | Fix the field named in `field` or the detail in `message`. |
| 400 | `invalid_json` | Body is not parseable JSON or holds more than one publication. |
| 401 | `authentication_required` | Missing, wrong, rotated or revoked credential. |
| 403 | `browser_origin_forbidden` | Browser-origin writes are refused; use a server-side client. |
| 403 | `forbidden` | The key can only replace its own author's pages. |
| 404 | `not_found` | No such page or API route. |
| 409 | `slug_conflict` | Slug already exists; create with a different slug, never silently PUT over it. |
| 409 | `public_slug_unavailable` | The public path is taken. |
| 413 | `payload_too_large` | The request exceeded 16 MiB; reduce payload size. |
| 415 | `unsupported_media_type` | Send `application/json`. |
| 500 | `storage_unavailable` | Retryable; check reachability and the library before retrying. |
| 500 | `link_allocation_failed` | Retryable link generation failure. |
| 503 | `storage_cleanup_pending` | Retryable; fix storage permissions. |
| 503 | `public_sharing_unavailable` | Retryable; sharing is not configured. |

A successfully saved replacement can include a `warning` when old temporary files could not be removed. Cairn retries cleanup before another publication and on restart; fix storage permissions before backing up if this warning appears. Never return a success link to the owner after a failed request. Retrying creation at the same supplied slug cannot overwrite it; use explicit replacement deliberately. A timed-out POST might already have saved a page, so check the library before retrying; there is no idempotency key yet.

## API index and reading routes

`GET /api` is a private index with the API version, endpoints, limits and examples in JSON. It serves the same contract as `internal/cairn/contract.md`, which is also rendered into Settings agent setup text.

- `GET /{slug}` renders the private owner reader with title, byline, date and original-request context.
- `GET /_content/{slug}` renders report HTML only, without private metadata. It is the content route, not the page metadata API.
- `GET /_content/{slug}?asset=name.png` serves that page's managed image without needing the slug at publish time; `GET /_assets/{slug}/name.png` serves the same bytes once the slug is known.

## Reading and agent replies

The private short URL shows title, agent byline, date and expandable original-request context. Report HTML has its own sandbox, including direct content responses. Custom scripts can run but cannot access the parent or private metadata; forms, network requests and nested frames are blocked. The only external scripts permitted are Cairn's packaged runtime. Private metadata is not embedded in report HTML or images. Do not put private chat transcripts in the report content itself.

Reply to the owner with the full private link, one line describing it and a pointer to Cairn's root URL for the library. The root URL opens the searchable private library; `/import` accepts HTML/Markdown and optional images. Management forms support rename, archive/restore and one-confirmation deletion. They require an owner form token and same-origin request, not the publisher credential. On replacement, say what changed. Use sensible titles and keep the original request in the owner's words. Publisher credentials grant no public-sharing capability.

## Public links

Only the owner can enable sharing through private management or the editor's Save & share action. Sharing keeps the page's private path on the public origin. New pages default to three random characters from A-Z, a-z and 0-9, giving 238,328 combinations. Settings can select 3 to 7 characters for future pages. Generated links and custom names cannot collide with another page's private path or reserved public alias. Existing custom public aliases continue to work alongside the shared page's private path. Turning sharing off disables every public route for the page, including images and downloads, while its private Tailscale URL stays accessible. Rename preserves links, replacement updates their contents and archive does not revoke sharing. Public links are for content the owner is comfortable making discoverable; password protection remains optional. The public listener returns 404 for the library root, `/api` and every route beneath it, `/settings`, `/manage/`, `/new` and `/import`. Publisher credentials cannot enable sharing through the API.
