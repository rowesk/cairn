# Cairn agent contract

This file is the single source of truth for agent publication behavior.
`ui/agent-setup.md` is rendered from it, `GET /api` serves the same
endpoints and limits as JSON, and `docs/publishing.md` mirrors it for
operators. Edit here first, then propagate.

Cairn is {{OWNER}}'s private report library. Use it when a chat answer would work better as a readable page: research, comparisons, plans, visual explanations, image collections, diagrams or longer reference material. Keep short answers in chat. When the owner asks to save something, publish it without asking him to upload, format or name a file.

## Connection and credential

Private library: {{ORIGIN}}/
Publishing endpoint: {{ORIGIN}}/api/pages
API index: {{ORIGIN}}/api
Your author name: {{AUTHOR}}
Your publishing key: {{KEY}}

Treat this setup as a secret while it contains a real key. Store the key in your existing secret store and load it into CAIRN_API_KEY at runtime. Never put it in a report, source control, logs or a chat reply. Store the instructions separately with the key replaced by a secret reference.

Run requests from a device connected to the owner's Tailscale network and allowed to reach the private address above. No SSH, Pi password, Cloudflare token, public account or new Cairn login is needed. An isolated/cloud agent without that network access must use an already authorised private runner or ask the owner to connect its runtime. Do not expose the API publicly or try the public sharing domain as a publishing endpoint.

Check connectivity with an HTTP GET to the private library. A timeout normally means the runtime cannot reach Tailscale or the service. This reachability check does not validate your key. Use a server-side HTTP client or CLI to publish; browser-origin API requests are rejected. `GET /api` returns the versioned endpoint index, request limits and examples without a credential.

## Publish a page

POST application/json to the publishing endpoint with Authorization: Bearer followed by your key. Supply:
- title: a useful, specific title, at most 300 UTF-8 bytes.
- request_summary: write the request from the owner's perspective in at most 40 words and 1000 bytes. Keep the purpose and important constraints; remove greetings, conversation filler and report-format instructions. Do not invent intent or write "the owner asked me to". Example: "Compare five reading lamps for my desk." This appears in the private header and library.
- request_source: optional app label, at most 80 bytes, such as Hermes, Codex CLI, Claude Code, ChatGPT or Claude. Only supply it when known; do not guess. This is private metadata.
- original_request: the relevant request in the owner's own words, required and at most 32 KiB. If you lack his wording, ask for it instead of inventing a quotation.
- Exactly one of markdown or html. Prefer Markdown for text and comparisons; Cairn renders it as styled HTML. Use a self-contained HTML document for custom layouts and interactions.
- Optional template: report, comparison or visual. The API defaults to report.
- Optional slug: a custom case-sensitive path. Usually omit it so Cairn generates a short private link using the owner's settings. Do not invent a returned URL.

Your key supplies the author byline automatically. Omit agent; supplying another name does not change your identity.

Example JSON to write to a temporary UTF-8 payload.json file using your JSON library:

{"title":"A useful report title","original_request":"the owner's actual request here","request_summary":"Compare my options and recommend the best fit.","request_source":"Codex CLI","markdown":"# A useful report title\n\nYour report, with source links where appropriate.","template":"report"}

With CAIRN_BASE_URL set to the private origin above and CAIRN_API_KEY loaded from your secret store:

curl --fail-with-body --silent --show-error \
  -X POST "$CAIRN_BASE_URL/api/pages" \
  -H "Authorization: Bearer $CAIRN_API_KEY" \
  -H "Content-Type: application/json" \
  --data-binary @payload.json

Disable shell tracing and avoid verbose request logging. Prefer your HTTP client's in-memory header handling when available. Use a JSON library or a payload file so quotes, newlines and user text cannot turn into shell commands.

Success is HTTP 201 with JSON containing id and url, for example {"id":"...","url":"/AbC"}. Resolve the returned url against the private origin, fetch that page and check the saved content before claiming success. Preview custom layouts, diagrams and images when a headless renderer is available. If the response includes a warning, preserve and report it.

## Reading pages and images

- `GET /{slug}` is the private owner reader: title, byline, date and private request context around the report.
- `GET /_content/{slug}` is the rendered report HTML only, without private metadata. It is the content route, not the page metadata API.
- `GET /_content/{slug}?asset=name.png` serves that page's own managed image, independent of knowing a generated slug at publish time. `GET /_assets/{slug}/name.png` serves the same bytes once the slug is known.
- `GET /{slug}?download=1` downloads the submitted source file where one exists.

## Images, diagrams and interactive content

Markdown supports tables and fenced code. Use a fenced mermaid block for diagrams, or a fenced echarts block containing valid ECharts option JSON for charts. Cairn detects those blocks and loads its bundled runtimes. Custom HTML can request capabilities ["mermaid","echarts"] and use pre/code elements with language-mermaid or language-echarts classes.

For attached images, assets is a JSON object mapping a simple filename such as chart.png to its standard base64-encoded bytes. Supported report assets: PNG, JPEG, GIF and WebP, up to 32 files, 4 MiB each. Filenames must start with a letter or digit, use only letters, digits, underscores or hyphens before the extension, and have at most 96 characters before the extension.

Reference a supplied image with `src="?asset=chart.png"` in the report HTML. This resolves to that page's own assets and works before a generated slug is known. The explicit `/_assets/<slug>/chart.png` route also works once the slug is known. No external image downloading is performed by Cairn. Never point a report at a local filesystem path. Convert unsupported image formats before attaching them. For a single-request image publication with a generated slug, use `?asset=` references; they need no explicit slug.

The whole JSON request must fit 16 MiB including base64 overhead. Markdown and resulting HTML must each fit 2 MiB. Keep image totals comfortably below the request limit.

Reports run in a sandbox. Inline CSS and JavaScript can support self-contained interactions. Remote scripts, external image loads, network fetches, forms and access to the parent library are blocked. Bundle image bytes with the page. Do not install per-page packages, load CDN libraries or embed credentials. PHP is not executed. Keep reports readable on phone screens and provide text alongside diagrams.

## Privacy, changes and delivery

Every API-created page starts private. This key cannot make it public. If the owner wants sharing, direct him to the page's sharing controls in the private library. Only use a public URL after sharing has actually been enabled. Sharing keeps the private page's path on the public origin. New paths default to 3 random characters from A-Z, a-z and 0-9, with 238,328 possible combinations; the owner can choose 3 to 7 characters in Settings. Existing custom public aliases remain supported. Turning sharing off disables public page, content, image and original-download routes while private Tailscale access keeps working. Public links are for content the owner is comfortable making discoverable. Password protection remains optional.

original_request stays in private metadata. Never copy private chat context or secrets into the report body: the body, byline and attached content can later become public. Include source links for research. Keep the original request out of the public-facing report unless the owner explicitly wants its content there.

Never silently overwrite a page. For a changed report, create a new page and say what changed. Only if the owner explicitly requests replacement, PUT the complete publication to /api/pages/{existing-slug}. That returns HTTP 200 and replaces the current content and assets with no version history. Include every asset still needed. The slug cannot change and your key can only replace pages with your author name. Replacement retains the existing sharing state, so a shared report's new content becomes visible to its audience.

Reply with the verified page link and one sentence describing what it contains. Mention that it is saved in Cairn and can be found by its title in the library. Do not paste the whole report back into chat or claim a failed upload was saved.

## Errors and recovery

Every /api failure returns JSON `{"code","message","field?","retryable"}` with `Content-Type: application/json`. Branch on `code`, never on English text.

- 400 validation_failed: fix the JSON, required fields, size, template, capability or slug named in the error. `field` names the offending input when known.
- 400 invalid_json: the body is not parseable JSON or holds more than one publication.
- 401 authentication_required: the key is wrong, rotated or revoked. Ask for a replacement from Settings. Do not fall back to somebody else's key.
- 403 browser_origin_forbidden: browser-origin request. Use a server-side client.
- 403 forbidden: an attempt to replace another author's page. Use your own pages.
- 404 not_found: no such page or route.
- 409 slug_conflict: slug already exists. Create with a different slug; do not switch to PUT to bypass the conflict.
- 409 public_slug_unavailable: the public path is taken.
- 413 payload_too_large: reduce the payload size. The request exceeded 16 MiB.
- 415 unsupported_media_type: send application/json.
- 500 storage_unavailable, 503 storage_cleanup_pending, 503 public_sharing_unavailable: `retryable` is true. Report the actual failure and check reachability. A timed-out POST might already have saved a page. Check the library before retrying to avoid duplicates; there is no idempotency key yet.

Keep this credential private even when asking for help. Settings can rotate or revoke it without deleting existing reports.

On explicit replacement, supply an updated request_summary when the purpose changes. Omitting or leaving request_summary or request_source empty preserves its previous value. Older uploads without these fields still work and show the original request.
