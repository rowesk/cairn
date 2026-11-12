# Cairn

Cairn keeps pages, written by the owner's agents or by the owner, in the owner's private, searchable library.

## Language

The words Cairn's screens, messages and documentation use, one per idea. Field names in the API keep their own spelling (`slug`, `agent`, `original_request`); the words around them follow this list.

| Say | For | Not |
|---|---|---|
| page | the thing in the library: a title, a byline, a date, the request behind it and its content | report (except for the Report template, and for "report" as what a page says) |
| Private, Shared | a page's two states | Public, Link access, Public link, Anyone with the link |
| public link | the address of a shared page | public URL, public path, public alias |
| link name | the last part of a page's address, which the owner can choose | slug (except as the API field), Public URL name |
| author | whoever a page is by: an agent or the owner | person, people, creator |
| Prepared by | the label in front of a byline | By, Created by |
| your request | the request, in the owner's own screens | reason, original prompt, prompt |
| original request | the request, in documentation and the API | reason, original prompt |
| agent key | an agent's credential | API key, token (alone) |
| installation token | the credential `cairn init` writes | publisher token |
| theme | how Cairn looks: System, Cairn, Dark, Minimal or Futuristic | preset, skin, admin view |

### Page

A saved answer meant to be read outside chat, with a title, its author, its creation date and the request that prompted it. Agents publish pages through the API; the owner writes them in the editor or imports a file.

### File page

A page whose content is one stored file, shown with a filetype icon, its name, size and a download button. Cairn keeps the file as uploaded and never opens or runs it.

### Library

The owner's private collection of pages. Sharing a page does not grant access to the library.

### Private page

A page only the owner's authorised devices can open. New pages start private.

### Shared page

A page the owner has made readable through a public link, optionally behind a password. The owner can stop sharing at any time.

### Public link

The address of one shared page on the owner's public domain. Its last part is the page's link name, readable or random. It grants no access to the library or to other pages.

### Link name

The last part of a page's address, such as `heat-guide` in `https://reports.example.com/heat-guide`. Cairn picks a short random one unless the owner or an agent chooses one. The API calls it `slug`.

### Template

A layout and style for a page written in Markdown: Report for reading, Comparison for tables and choices, Visual for images and diagrams. Agents use a suitable template by default and can write their own HTML when the content needs it.

### Author

Whoever a page is by: one of the owner's agents, or the owner. The byline names the author after "Prepared by", on private and shared views alike. The API calls it `agent`.

### Original request

The owner's words that prompted a page, kept as private context. The owner's screens call it "your request". Sharing a page never shares it, and neither does Send to Kindle.

### Archived page

A page hidden from the library list but kept in the archive, where its links still work.

### Theme

How Cairn looks, chosen once in Settings for every view: System (Cairn by day, Dark by night, following each device), Cairn, Dark, Minimal or Futuristic. Shared pages can have a theme of their own. Pages written in Cairn take the theme's colours; pages in a design of their own keep it.

### Kindle delivery

An EPUB copy of a page emailed from the owner's mail account to their Kindle address. It carries the page and its byline, never the original request.

### Agent key

A credential for one agent, made in Settings under Authors. Through the API it publishes private pages under its own byline and lists, reads, replaces and archives only those pages; it cannot delete or change existing sharing. It is a byline, not a lock: it does not keep software that can reach the private address out of the library. Cairn keeps only its fingerprint and shows it once, and makes none for the owner's default author.

### Installation token

The credential `cairn init` writes to the data directory, in the file `publisher-token`, for the owner's own scripts. It reaches every page and names the byline in each publication.
