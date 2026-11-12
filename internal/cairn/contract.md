<!--
Maintainer notes. Nothing above the marker line below ever ships.

This file is the agent setup text. Settings copies everything below the
marker for the owner to paste into an agent's instructions, and
ui/agent-setup.md is a checked copy of that part for people reading the
repository (contract_test.go keeps them equal and proves no secret or
maintainer note can reach the copied text).

Two placeholders are filled in the browser: {{ORIGIN}} with the server's
private base URL, or the address Settings was opened at, and {{AUTHOR}}
with the byline of the person whose key dialog is open.

Rules for editing:
- Never put a key, or a placeholder for one, in the text. Agents read
  $CAIRN_API_KEY; the key itself is copied once from the key dialog.
- Keep it under about 700 words, in the order an agent needs it.
- Keep it deployment-neutral and gender-neutral: "the owner", "their".
- Field names, limits and codes must match api.go, errors.go and
  docs/publishing.md. GET /api and /api/openapi.json are generated from
  the code, so they cannot drift; this text can.
-->
<!-- agent setup text starts here -->
# Publishing to Cairn

Cairn keeps pages for the owner. Publish when asked; keep short answers in chat. Publish yourself, without asking the owner to upload a file.

## Connect

- Library: {{ORIGIN}}
- Your byline: {{AUTHOR}}

Read two environment variables: `CAIRN_URL`, the library address, and `CAIRN_API_KEY`, your agent key. The owner gives you the key separately; keep it in your secret store and never write it into a page, file, log, command or reply.

Send requests from a machine that can open the library, with a command-line or server-side client; browsers are refused. `GET $CAIRN_URL/api` describes the API without a key.

## Publish

Write JSON with a JSON library:

```json
{
  "title": "Desk lamps under £100: three worth buying",
  "original_request": "can you find me a decent desk lamp under 100 quid, warm light, no flicker",
  "request_summary": "Find a warm, flicker-free desk lamp under £100.",
  "request_source": "Claude Code",
  "template": "comparison",
  "markdown": "Three lamps stand out.\n\n## The shortlist\n\n| Lamp | Price |\n|---|---:|\n| BenQ e-Reading | £89 |"
}
```

```sh
curl -sS -X POST "$CAIRN_URL/api/pages" \
  -H "Authorization: Bearer $CAIRN_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: lamps-1" \
  --data-binary @page.json
```

Cairn answers `201` with the page: `{"slug": "k3T", "url": "/k3T", "private_url": "…/k3T", "warnings": [], …}`. Where the `cairn` command is installed, `cairn publish page.md -title "…" -request "…" -summary "…"` does the same.

## Title and request

- `title`: says what the page covers or concludes. "Desk lamps under £100: three worth buying", not "Lamp research".
- `original_request`: the owner's own words. If you only have a paraphrase, ask for their wording rather than invent a quotation.
- `request_summary`: at most 40 words from the owner's side, keeping purpose and constraints: "Find a warm, flicker-free desk lamp under £100." Never "The user asked me to…".
- `request_source`: your app's name, such as Claude Code, only when you know it.

Your key sets your byline, so leave out `agent`.

## Choose a template

- `report` (the default): reading and notes, everything in one column.
- `comparison`: pages built around tables; tables widen and rows shade.
- `visual`: images, diagrams and charts; figures widen.

Send `html` instead only when the page needs its own layout or scripts; it runs in a sandbox with no network access.

## Write the Markdown

GitHub-flavoured Markdown, plus:

- Footnotes: `claim[^1]`, then `[^1]: The source.`
- Callouts: a quote starting `> [!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]` or `[!CAUTION]`, and `[!VERDICT]` or `[!SUMMARY]` for a conclusion.
- Images: add `"assets": {"lamp.png": "<base64>"}` (PNG, JPEG, GIF or WebP; 4 MiB each, 32 at most) and write `![What it shows](?asset=lamp.png "Caption")`.
- Diagrams: a fenced `mermaid` block.
- Charts: a fenced `echarts` block holding one JSON object of ECharts options. Invalid JSON is refused with its line.
- Raw HTML: text elements, tables, `details` and `figure` stay. Scripts, styles, event handlers, forms, frames and SVG are removed and listed in `warnings`.

Don't open with a `# heading` repeating the title; Cairn shows the title above the page. Use `##` for sections.

## Update a page

Only when the owner asks. Find it with `GET /api/pages?q=lamps`, read it with `GET /api/pages/{slug}` (it includes your Markdown), then `PUT /api/pages/{slug}` with the whole new page and every image it still needs. Its address, date and sharing stay. Your key changes only pages with your byline; use only the API, never the library's own pages. `PATCH` with `{"archived": true}` archives one; only the owner deletes pages or changes existing sharing.

## Errors

Failures are JSON with `code`, `message` and `errors`, which lists every problem with its `field`, `limit` and `allowed` values. Fix them all, then send again.

- 400 `validation_failed`: fix each entry in `errors`.
- 400 `invalid_json`: the body is not one JSON object; `message` gives the line.
- 401 `authentication_required`: no key was sent. 401 `invalid_credential`: the key is wrong, rotated or revoked; ask the owner for a new one.
- 404 `not_found`: no page your key reaches has that slug.
- 409 `slug_conflict`: that slug is taken; leave `slug` out.
- 413 `payload_too_large`: the request is over 16 MiB; send smaller images.
- `retryable: true`: send it again with the same `Idempotency-Key`, which never saves twice.

## Afterwards

Return the full address and one sentence. Mention `dropped_html` warnings. Never claim a failed save succeeded.

## Sharing a new page

Publish privately by default. Set `share_enabled` to `true` on POST only when the owner asked for this page to be shared. Never otherwise. Tell the owner the saved `public_url` afterwards. Omitted or false stays private; null is refused. PUT refuses this field and preserves sharing, link names and passwords. Without configured sharing, creation fails with 503 `public_sharing_unavailable`. Files start private.
