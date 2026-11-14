# Writing and importing

Choose **New page** in the library to write a title and Markdown. The arrow beside it opens the choice between writing and importing. The editor saves private pages with the default author's byline. Preview uses the same renderer as the saved page, in your library's theme. Command or Control and S saves. Leaving with unsaved writing triggers the browser's warning.

Cairn keeps a page's Markdown beside its rendered HTML, in the same stored object, so you can edit the page later and download the Markdown from your own reader. There is no version history: saving replaces the page.

## Markdown

Cairn renders GitHub-flavoured Markdown: headings, lists, task lists, tables, strikethrough, links and fenced code. It also renders the following.

- **Footnotes.** Write `text[^1]` and, anywhere below, `[^1]: The note.` Notes collect at the end of the page, numbered, with a link back to each reference. They survive into Send to Kindle.
- **Images.** Attach the file as an asset and place it with `![What the picture shows](?asset=bulb.png)`. The alt text is what a screen reader says and what shows if the image cannot load. An image alone in its paragraph becomes a figure; a title becomes its caption: `![Alt text](?asset=bulb.png "The caption")`. Two or more figures in a row sit side by side where there is room. You can also use a web URL, such as `![The ridge](https://example.com/ridge.jpg)`, or a raw `<img src>` in Markdown. Cairn downloads it when you save, stores it with the page and keeps the original URL in your Markdown. Re-saving reuses the stored image while the URL stays the same. Preview may show a broken image until save. If a fetch fails, the saved page shows the alt text linked to the original URL.
- **Callouts.** A quote that starts with `[!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]` or `[!CAUTION]` becomes a labelled note, as on GitHub. Cairn adds `[!VERDICT]` and `[!SUMMARY]` for the conclusion of a comparison, set larger than the text around it.

  ```
  > [!VERDICT]
  > Buy the warm LED. It pays for itself in under a year.
  ```

- **Code.** A fence that names its language, such as ` ```sh `, shows the language above the block. Code keeps its lines and scrolls sideways when it is wider than the page; `text`, `markdown`, `log` and `output` blocks wrap instead. Cairn does not colour code.
- **Diagrams.** A ` ```mermaid ` block draws a Mermaid diagram in the page's type and colours. On a phone a large diagram stops shrinking when its smallest text reaches 12px and scrolls sideways instead, with a note saying so. A diagram that Mermaid cannot draw shows a short message and its source.
- **Charts.** A ` ```echarts ` block holds one JSON object of ECharts options. Cairn checks it when the page is published and refuses the page if it is not valid JSON, naming the block and the line: `echarts block 2 on line 31 is not valid chart JSON`. Keys need double quotes, and comments, functions and trailing commas are not JSON. Charts use the page's type and colours, size themselves from their data and the width they have, keep every label at 12px or more, and adjust margins, ticks and legends on a phone. Options you set yourself win, except font sizes under 12px and, on a phone, side margins wider than about a fifth of the chart.
- **Quotes and dashes.** Straight quotes and apostrophes become curly ones, `---` becomes a dash and `...` an ellipsis. Code is never changed, and `--` stays as two hyphens.

### Raw HTML

Markdown may carry a little HTML for things Markdown cannot say. Cairn keeps these elements:

| Kind | Elements |
| --- | --- |
| In a line of text | `a`, `abbr`, `b`, `bdi`, `bdo`, `br`, `cite`, `code`, `data`, `del`, `dfn`, `em`, `i`, `img`, `ins`, `kbd`, `mark`, `q`, `s`, `samp`, `small`, `span`, `strong`, `sub`, `sup`, `time`, `u`, `var`, `wbr` |
| Blocks | `aside`, `blockquote`, `details`, `summary`, `div`, `dl`, `dt`, `dd`, `figure`, `figcaption`, `h1` to `h6`, `hr`, `ol`, `ul`, `li`, `p`, `pre`, `section` |
| Tables | `table`, `caption`, `colgroup`, `col`, `thead`, `tbody`, `tfoot`, `tr`, `th`, `td` |

Every element may keep `id`, `class`, `title`, `lang`, `dir`, `aria-label`, `aria-hidden`, `aria-describedby` and `aria-labelledby`. Some keep more: `href` on links; `src`, `alt`, `width` and `height` on images; `colspan`, `rowspan`, `scope` and `align` on table cells; `start`, `reversed` and `type` on ordered lists; `open` on `details`; `datetime` and `cite` where HTML defines them. Links may point at web, mail and phone addresses, at `#` anchors and at relative paths such as `?asset=bulb.png`. Images may use relative paths, web addresses and inline PNG, JPEG, GIF, WebP or AVIF data.

Everything else is removed: scripts, styles and `style` attributes, event handlers such as `onclick`, `javascript:` and other unsafe addresses, frames, forms, SVG and MathML, and anything Cairn does not recognise. Elements that are only wrappers, such as `<font>` or a custom element, are removed but their text stays. A publication through the API lists what was removed in a `dropped_html` warning, so the agent can tell you. Use custom HTML instead of Markdown for anything beyond this list.

### Templates

All three Markdown templates set the text in the same serif column, about 68 characters wide, with headings, tables, captions and charts in the system sans. They differ in what may leave that column.

| Template | For | What changes |
| --- | --- | --- |
| `report` | Long reading, the default | Everything stays in the column. Wide tables scroll inside it. |
| `comparison` | Pages built around tables | Tables may use the whole width of the screen, centred on the column, with shaded alternate rows and the first column set as row headers. The contents open from the reader's bar rather than beside the text. |
| `visual` | Pages built around pictures, diagrams and charts | Figures, diagrams and charts widen up to 84rem; captions centre beneath them. |

In every template, columns of numbers align right with figures of equal width, short and numeric columns take only the room their values need (their headings wrap above them), the first column keeps room for a name, and a table, code block or diagram that is wider than the screen scrolls sideways inside the page with a fade or shadow at the edge that has more to show. Keyboard users can tab to a scrolling region and use the arrow keys. On wide tables the first column stays in place while the rest scroll. A diagram wider than the column may widen past it, up to 54rem, in every template. Gantt charts draw to the column's width, with dates as "1 Mar"; on a phone they keep a readable width and scroll. Diagrams load when you scroll near them, so a long page opens quickly; printed, a diagram not yet drawn shows its source. Every chart carries its data as a table for screen readers, and every diagram a description of its steps, messages, tasks or slices.

`template` applies only to Markdown. With `html`, Cairn stores the HTML exactly as sent and ignores `template` without an error.

### The page title

The reader's cover already shows the page title. When the Markdown opens with an `h1` that repeats the title, or is its start (`# Monitors` for the title "Monitors: which to buy"), Cairn keeps the heading in the page for screen readers, Send to Kindle and anyone opening the page's content directly, but hides it inside the reader and leaves it out of the contents list. Case, spacing, curly quotes and closing punctuation do not matter. A heading that says something the title does not, or that comes after other text, shows as written.

### Pages saved before this version

Cairn stores each page as rendered HTML. Pages saved before these changes keep the look, footnote links and HTML handling they were saved with, and keep their light colours in a dark theme; only new and replaced pages use the current templates and follow the theme.

## Imports

Import accepts any file up to 512 MiB. Cairn opens a file as a page when it can: UTF-8 HTML up to 25 MiB, other UTF-8 text up to 2 MiB and DOCX up to 10 MiB. Images become file pages. Owner file uploads are shared on creation. Imported readable pages stay private. Everything else, including those types over their limits, becomes a file page with a filetype icon, the filename, size and a **Download** button. Tick **Save as a file page, even if Cairn can open it** to keep a file you could otherwise read as a page, such as a Markdown file you want to hand on unchanged. Supporting images must fit 4 MiB each.

| File | Reading view |
| --- | --- |
| Markdown, HTML | Existing Cairn renderer or sandboxed HTML |
| PNG, JPEG, GIF, WebP, SVG | File page with a direct image address |
| TXT, JSON, CSV, XML, YAML, logs | Escaped source text; valid JSON is indented |
| Mermaid, MMD | Diagram using Cairn's bundled Mermaid runtime |
| PHP | Escaped source text, never server execution |
| DOCX | Extracted paragraph text; original retains formatting and embedded content |
| Other files, including ZIP, PDF, DOC, ODT, RTF, spreadsheets, video and large images | A file page with a Download button |

A blank title comes from the file: a Markdown file's first `#` heading or an HTML file's `<title>`, as `cairn publish` reads them, and otherwise the file name. The author and your request have defaults; **More details** lets you change them. Every import keeps the original file. File pages offer Download to the owner and visitors with access. Other page sources stay owner-only, except shared HTML imports, which also have a sandboxed direct HTML address. From the library's preview, **Open page** reaches the reader and the download.

## Storage and access

Uploads stream to `uploads/` inside the data directory and are renamed into the page object. Cairn clears interrupted uploads at startup. File pages add `file_page`, `file_name` and `file_size` columns; existing pages stay unchanged.

Original files use fixed internal names, `source.bin` and `source.json`, within the page's object directory. The uploaded filename is metadata and never a filesystem path. The Download action streams originals as `application/octet-stream` with attachment disposition, `nosniff`, a restrictive CSP and `no-store`.

Shared file pages allow downloads behind the same password checks. Other original files stay owner-only. Deleting the page removes its original with the rest of its stored object. No PHP interpreter, office conversion process or additional Pi service is required.

## Writing and editing pages

**New page** opens the editor. Write a title, then Markdown in the body, which uses a monospace face so tables and fences line up. Both fields grow with what you write. **Write** and **Preview** switch the same space between the writing and the page as it will look, with the title on a band in the reader's cover colours. From 1200px wide, **Side by side** shows both, and the preview follows the writing as you type. Command or Control and P switches between writing and preview; Command or Control and S saves.

Missing titles, empty pages and address problems show beside their field when you save, in Cairn's words. Nothing is sent until they are fixed.

**Private** in the toolbar opens Page access: **Private** (only your devices) or **Shared** (anyone with the link; your library stays private). It shows the public link the page will have, and while you type a link name it checks it against your other pages, so a name another page uses says so before you save. A blank link name lets Cairn choose one. Choosing Shared changes the save button to **Save & share**. Shared is only available once the public listener is configured.

Paste PNG, JPEG, GIF or WebP images into the body. Cairn inserts an image reference and attaches the image to the page. Each image must fit 4 MiB, up to 32 images and 12 MiB in all.

The browser keeps unsaved writing for each page separately, so a reload or a crash restores it with **Draft restored from 13:57** and a **Discard draft** button. Times are 24-hour, in your device's time zone. Saving clears the draft.

### Editing a saved page

Pages written in the editor, and pages imported from a Markdown file, keep their Markdown. Open **Edit** from the page's menu in the library, from Manage page, or from the reader. The editor reopens with the title, Markdown, images and access. **Save changes** replaces the page and returns to it. The page keeps its address, its date, its sharing and link name, and its password. Images stay while the Markdown still refers to them by name or original web URL. Choosing Private in Page access stops sharing; choosing Shared shares it at the link name shown.

Markdown that agents send through the API is kept too, so their pages can be edited here. Pages Cairn cannot edit say why on Manage page in one sentence: custom HTML, Markdown an agent sent before Cairn kept a copy, or a file imported in another format such as a Word document. Ask the agent for a new version, or change the original and import it again.

Pages you write or import have no original request, so the reader shows no request block for them. Pages published through the API still need one.

## Finding pages

The library lists pages fifty at a time, newest first; **Show more pages** adds the next fifty in place, and works as a plain link without JavaScript. A dot beside a title marks a page you have not opened yet. Opening it in the reader or the preview clears the dot. Only your own reading on the private address counts; visitors on a shared link never do.

Search looks through titles, your requests and their summaries, authors and the words of every page, as you type. Results update in place and the address follows, so a search can be bookmarked and Back returns to where you were. Titles that match come first, then requests, then authors, then the page's own words; choose **Newest first**, **Oldest first** or **Title A to Z** to order them differently. A result found outside its title shows the matching words, marked, and says where they were.

- Every word must match, in any order and any field.
- Each word matches as the start of a word, so `quartz` finds "quartzite".
- Put words in double quotes to match them as a phrase: `"walnut worktop"`.
- Case and accents do not matter: `cafe` finds "Café".
- Punctuation is ignored, so nothing you type can break a search.

Each row's excerpt is the page's first real paragraph. Dates read as "Just now", "Yesterday" or "4 Nov" in your browser's own time zone, with the full date and time on hover. Cairn skips the opening heading that repeats the title, code, diagrams, charts, tables, navigation and footnotes. Existing pages get new excerpts and their search index in the background when Cairn starts after an upgrade; until a page is indexed, its title and request still match.

### Keyboard

| Keys | Does |
| --- | --- |
| `/` or Command or Control and K | Search |
| Escape | Clear the search |
| J and K, or the arrow keys | Next or previous page |
| Enter | Open the page |
| Space or P | Preview the page; J and K step through the list in the preview |
| E | Archive the page, or restore it in the archive, with Undo |
| Command or Control and Z | Undo the last archive while its notice shows |
| ? | Show the shortcuts |

Shortcuts never fire while you type in a field, and Tab moves through the page as usual. On phones a tap opens the full reader rather than the preview.

When you open a page from a search, an author's pages or a different order, the reader's previous and next page (and the [ and ] keys) follow that same list, and Back to library returns to it.

Each row's menu holds Copy link, Edit (for pages that can be edited), Share or Stop sharing, Archive or Restore, Send to Kindle when it is set up, and Manage page. Without JavaScript the menu button opens Manage page.

## Settings

Open **Settings** from the library to choose the default author, the Markdown editor's template and the link name length. The default author also applies to imports with a blank author; an author an agent sends, and a template chosen on import, stay as they were. Link names Cairn picks use 3 to 7 letters and digits, 3 by default, and never repeat one in use. Sharing a page keeps its link name on the public address, and stopping sharing leaves the private address working. Existing links never change with this setting.

**Appearance** sets the theme: System, Cairn, Dark, Minimal or Futuristic. One click on a card applies it everywhere and saves it; **Customise per view** gives the reader or shared pages a theme of their own. Pages written in Markdown take the theme's colours. See the [README](../README.md#themes) for what each theme looks like.

Author photos match bylines exactly. Choose WebP, AVIF, PNG, JPEG or another browser-decodable image up to 4 MiB, 4,096px per side and 16 megapixels. The photo dialog lets you position and zoom before saving. The browser sends the selected crop as PNG; Cairn validates and stores a 128px PNG in SQLite. Raw form uploads still use the server’s PNG/JPEG/GIF decoder. Photos appear in library rows and beside the byline in your reader and on shared pages; removing a photo restores initials. There is no publicly accessible profile directory or settings endpoint. Settings and photos persist in the normal SQLite backup.

## Agent keys

**Authors** is the first section of Settings, and the library's first-run "Connect an agent" link lands on it. Name the agent the way its byline should read and choose **Create key**. An empty name is caught beside the field. **Add a photo for an author without a key** gives an author a photo and nothing else.

Each row shows an author with their key's state: **No key**, or **Key active** with when it was created and last used, then **Manage key**. The last-used time counts every request the key authenticated, including ones whose page Cairn then refused.

The key dialog runs in order:

1. **Create a key.** Cairn shows it once, in a single-line field with **Copy key**. It stores only a SHA-256 fingerprint, so the key cannot be shown again. Closing the dialog without copying it asks first, with **Copy key** and **Close without copying**.
2. **Give the agent its instructions.** **Copy setup text** copies the agent setup text: when to publish, how to authenticate, one complete request, titles and summaries, templates, what Markdown supports, updating, errors and what to tell you afterwards. It names `CAIRN_URL` and `CAIRN_API_KEY` and holds no key, so it is safe to paste into a chat or a project file. **Copy command** copies a `cairn publish` line for agents that run commands. **Read the setup text** shows it first.
3. **Test it.** A `curl` command lists the agent's pages with its key. A reply starting `{"pages":` means the key works; `invalid_credential` means it did not arrive.

Reopening an active key shows steps 2 and 3, and **Rotate key** and **Revoke key**. Both confirm inside the dialog and say what happens: rotating stops the old key at once and shows a new one; revoking stops the agent publishing and reading, and keeps its pages. Focus starts on the dialog's first action, stays inside it, and returns to the button that opened it; changes are announced to screen readers.

Each name has one active key. An agent key signs pages with its name, ignoring any `agent` it sends, and through the API lists, searches, reads, replaces and archives only pages with that byline. It cannot delete pages or change existing sharing through the API, and it can replace pages you have shared, which visitors then see at once. Byline names also match pages written before the key existed, so give each agent a distinct name. Settings refuses a key on your own default author name, since that key would reach every page you write or import.

A key is a byline, not a lock. The library has no sign-in, so an agent that can reach the private address can do anything you can, key or no key. Run only agents you trust on machines that can reach it. [SECURITY.md](../SECURITY.md#security-model) explains the model. The installation token from `cairn init` still works for your own scripts; revoking agent keys does not affect it. Keys and setup text exist only on the private listener.

The setup text lives in `internal/cairn/contract.md`, below a marker that keeps the maintainers' notes out of it; `internal/cairn/ui/agent-setup.md` is its checked copy. [docs/publishing.md](publishing.md) is the full API reference.

## Links to other sites

Cairn adjusts absolute HTTP(S) and protocol-relative links when it serves a page, so they open in a separate tab with `noopener noreferrer`. This applies to every saved page, in your reader, on shared pages and in both previews. Section links and links to the page's own images stay as they are. The page's frame lets those tabs open outside its sandbox; the page itself keeps its opaque origin, blocked forms and restricted network access. Stored HTML and original files are not rewritten.

### Short requests and source apps

Agents should send `request_summary`, a rewrite from the requester's perspective in at most 40 words and 1000 bytes, alongside the required verbatim `original_request`. Keep intent and important constraints; remove conversational filler and formatting instructions. The private reader and library show the summary, falling back to the original for older uploads. Search matches both.

Optional `request_source` identifies the known app, such as Hermes, Codex CLI, Claude Code, ChatGPT or Claude. It is a single label of at most 80 bytes. Do not guess it. Your reader shows it in the request's label: "Your request · via Claude Code · only you see this". Shared pages show nothing of the request, the source included. The full original stays in SQLite.

On PUT replacement, omitted or empty summary/source fields retain their saved values. Supply a new summary when the purpose changes. Cairn does not generate summaries itself.

Publish privately by default. Set `share_enabled` to `true` on `POST /api/pages` only when the owner asked for this page to be shared. Never set it otherwise. Tell the owner the saved `public_url` afterwards, as well as the private address. Sharing must be configured or creation fails with 503 `public_sharing_unavailable` without saving. Omitted or `false` stays private. Any supplied value on PUT is refused; replacement preserves sharing, link names and passwords. Files start private.

## Files and links in the library

Pages, Files and Links have separate tabs, each with Shared and Archive filters. Search stays in the selected tab. When it finds no matches there, Cairn offers links to matches in other tabs or the archive.

File pages show previews for images, PDFs, video and audio. Copy direct link copies the stored file address, such as `/abc.pdf`. HTML imports up to 25 MiB remain report pages and also have direct `.html` addresses. Larger HTML uploads become file pages. Direct HTML and SVG use a sandbox without `allow-same-origin`, with network connections and form submissions blocked. Download always keeps the original bytes.

New link in the Links tab takes a target URL and optional link name. Cairn accepts HTTP and HTTPS targets, refuses credentials and local public link paths, and never fetches the target. Owner-created links are shared immediately. Open a link in the private library to edit its target, copy its public address, stop sharing, archive or delete it. Links have no passwords. Archiving keeps their public redirects working; stopping sharing returns 404 publicly.

Settings includes Storage, with total stored bytes, pages, files, everything else, free disk space and the ten largest files. Archived files still use space. The figures count stored bytes rather than filesystem allocation blocks.
