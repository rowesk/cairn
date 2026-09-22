# Writing and importing

Choose **New page** in the library to write a title and Markdown. The arrow beside it opens the choice between writing and importing. The editor saves private pages with the configured default author byline. Preview uses the same Markdown and Mermaid renderer as the saved page. Cmd/Ctrl+S saves. Leaving with unsaved writing triggers the browser's warning.

Saved Markdown can be downloaded from the full-page reader. Cairn keeps the source beside its rendered HTML, inside the same stored object. No history or draft service is added. The editor creates a page; it does not edit existing pages.

## Imports

The main upload limit is 10 MiB. Text input must be UTF-8 and at most 2 MiB. Images must fit 4 MiB each. The total multipart request, including supporting images, must fit 16 MiB.

| File | Reading view |
| --- | --- |
| Markdown, HTML | Existing Cairn renderer or sandboxed HTML |
| PNG, JPEG, GIF, WebP | Image page |
| TXT, JSON, CSV, XML, YAML, logs | Escaped source text; valid JSON is indented |
| Mermaid, MMD | Diagram using Cairn's bundled Mermaid runtime |
| PHP, SVG | Escaped source text, never server execution |
| DOCX | Extracted paragraph text; original retains formatting and embedded content |
| PDF, DOC, ODT, RTF | Document page with the original available for download |

A blank title uses the filename. Author and private context have defaults; optional details let you change them. All imports retain the original bytes. Full-page readers show **Download original**. Documents viewed in the library preview can be opened with **Open page** to reach the download.

## Storage and access

Original files use fixed internal names, `source.bin` and `source.json`, within the page's object directory. The uploaded filename is metadata and never a filesystem path. Cairn streams originals as `application/octet-stream` with attachment disposition, `nosniff`, a restrictive CSP and `no-store`.

Public downloads pass the same explicit sharing and password checks as their page. Unsharing or deleting the page removes access to its original too. Deletion removes the entire stored object. No PHP interpreter, office conversion process, database migration or additional Pi service is required.

## Editor links, sharing and pasted images

Click **Private** beside Preview and Save to choose a shortlink and who can read the page. The toolbar shows the selected access; its popover keeps the controls outside the writing canvas. Selecting public changes the save action to **Save & share**. A blank shortlink generates one. The same chosen path is used for the private page and its public link when sharing is selected. New pages default to **Only my devices**. **Anyone with the link** is available only when the public listener is configured. Public creation writes the page and its sharing state together; an unavailable link does not leave a partially saved page.

Paste PNG, JPEG, GIF or WebP images into the Markdown body. Cairn inserts an image reference and attaches the bytes to that page. Preview shows the image before anything is saved. Each image must fit 4 MiB; the editor accepts up to 32 images totalling 12 MiB. Unsupported images show an inline message. Pasted images remain attached when a normal validation error returns the editor. The title and writing canvas use the text caret without a surrounding focus box; buttons and other controls keep their focus indicators.


## Settings

Open `/settings` from the library to choose the default author, Markdown editor template and random link length. The default author also applies to imports with a blank author. Explicit publisher metadata and import template choices remain unchanged. Automatic page paths use 3 to 7 case-sensitive characters from A-Z, a-z and 0-9, default 3; generated paths retry collisions. Sharing uses the same page path on the public domain. Revoking sharing preserves private Tailscale access. Existing links never change with this preference.

Author photos use exact byline names. Choose WebP, AVIF, PNG, JPEG or another browser-decodable image up to 4 MiB, 4,096px per side and 16 megapixels. The photo dialog lets you position and zoom before saving. The browser sends the selected crop as PNG; Cairn validates and stores a 128px PNG in SQLite. Raw form uploads still use the server’s PNG/JPEG/GIF decoder. Embedded photos appear in library rows and private/public reader headers; removing a photo restores initials. There is no publicly accessible profile directory or settings endpoint. Settings and photos persist in the normal SQLite backup.

## Per-agent publishing keys

In Settings, use **Set up key** beside a person or agent. A new agent can be added by name without a photo. Create copies a one-time key and an agent setup snippet. Store the key in the agent's secret store. Cairn stores its SHA-256 hash, not the key itself.

Each name has one active key. Rotate replaces it immediately; revoke disables publishing through that key. The dialog asks for a second click before either action. Existing pages remain available. The dialog shows key creation time and last authenticated use, including requests whose publication payload is later rejected.

Send `Authorization: Bearer <key>` to the private `/api/pages` endpoint. Agent keys override the payload's `agent` field with the configured author name. They can create private pages and explicitly replace pages with that same author name. They cannot replace another author's pages. Public sharing remains an owner action. Author names also match pre-existing pages, so use a distinct name for each agent whose replacement access should be separate.

The existing installation publisher token remains valid for current integrations. Per-agent revocation does not disable that installation token. Key controls and setup snippets are only available through private Settings, never the public listener. Closing the dialog clears the one-time key from the page; it cannot be retrieved later.

**Copy agent setup** includes the private origin, author, one-time key, when to publish, Tailscale requirements, a JSON/curl example, supported content and limits, privacy rules, explicit replacement behavior and error recovery. The canonical guide lives in `internal/cairn/contract.md`; `internal/cairn/ui/agent-setup.md` is its checked copy. Agents should store the credential separately from reusable instructions. Reopening an existing identity's key dialog provides the same guide with a secret-store placeholder; it does not reveal or rotate the existing key.

## External report links

Cairn adjusts absolute HTTP(S) and protocol-relative anchor/image-map links when serving report HTML. They open a separate tab with `noopener noreferrer`. This applies to existing saved reports, private and public readers, library previews and Markdown editor previews. Section links and relative asset links stay unchanged. The report iframe and response policy allow popups to open outside the report sandbox; the report itself retains its opaque origin, blocked forms and restricted network access. Stored HTML and original files are not rewritten.

### Short requests and source apps

Agents should send `request_summary`, a rewrite from the requester's perspective in at most 40 words and 1000 bytes, alongside the required verbatim `original_request`. Keep intent and important constraints; remove conversational filler and formatting instructions. The private reader and library show the summary, falling back to the original for older uploads. Search matches both.

Optional `request_source` identifies the known app, such as Hermes, Codex CLI, Claude Code, ChatGPT or Claude. It is a single label of at most 80 bytes. Do not guess it. The private desktop reader shows it beside the format; phones hide that format line. Public readers omit all request metadata, including the source. The full original stays in SQLite.

On PUT replacement, omitted or empty summary/source fields retain their saved values. Supply a new summary when the purpose changes. Cairn does not generate summaries itself.
