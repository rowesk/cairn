# Public sharing

Public sharing is optional. Private reading and agent publishing work without a public domain. Pages stay private by default. When the owner explicitly asks for a shared page, an agent can send `share_enabled: true` to the private `POST /api/pages` endpoint. Cairn saves it as shared and returns both its private `url` and absolute `public_url`. Without a configured public origin, the request fails without saving a page. The API cannot change existing sharing, link names or passwords; replacements preserve those settings. Owner file uploads and owner-created links are shared on creation. Agent-created files and links stay private unless the request sets `share_enabled: true`. See [publishing](publishing.md) for the request and error contract.

Cairn uses two listeners. Port 8080 in the examples is the private library, editor, Settings and publishing API. Port 8081 serves only shared pages, their content and images, the password screen, and Cairn's own stylesheet, fonts and scripts. Never route a public hostname to the private listener.

## Configure the share-only listener

Use a hostname you control, such as `reports.example.com`. In `/etc/cairn/cairn.env`, set:

```sh
CAIRN_LISTEN=127.0.0.1:8080
CAIRN_PUBLIC_LISTEN=127.0.0.1:8081
CAIRN_PUBLIC_BASE_URL=https://reports.example.com
```

Restart Cairn. For a foreground process, use `-public-listen 127.0.0.1:8081 -public-base-url https://reports.example.com` alongside the usual data and credential flags.

Configure your HTTPS reverse proxy to forward that hostname to `http://127.0.0.1:8081`. Preserve the original Host header and don't cache pages. The public listener answers only the host of `CAIRN_PUBLIC_BASE_URL` and loopback addresses, so a proxy that sends any other Host gets "This link isn’t working" and a `host refused` line in the log. Password cookies require HTTPS. Cairn does not provision DNS, certificates or proxy services.

A Cloudflare Tunnel can route the hostname to `http://127.0.0.1:8081`; bypass the cache for that hostname. If you want a cache, only `/_ui/` and `/_runtime/` addresses that carry a version (`?v=`) are safe to keep: their names change whenever their contents do. For a Caddy server on the same host, a minimal example is:

```caddyfile
reports.example.com {
    reverse_proxy 127.0.0.1:8081
}
```

Configure your DNS and firewall for the proxy you choose. The backend remains bound to loopback. Do not share the data directory with a static file server.

## Verify before sharing

The public root, `/settings`, `/new`, `/import`, `/api` and `/api/pages` must return 404. The root deliberately has no index of shared pages.

Create a disposable page privately. Turn on Sharing in Manage page and check that its public link works with the same link name. Add an image and check it loads. Set a password of at least 8 characters and test it in a fresh browser session. Turn sharing off: the page, its content and its images must stop working on the public address, while the private address still works. Delete the disposable page afterward.

## What a public link grants

Link names Cairn picks are three letters and digits, mixed case, by default; you can choose 3 to 7 for future pages in Settings, or name any page yourself. Short links can be guessed, so they are not secrets: use a password if the page needs one. A password is one shared password, not individual accounts.

Sharing keeps the page's link name on the public address. A link name you change in Manage page replaces the old one; the page's original short address also keeps working while the page is shared. Stopping sharing turns off every public link to the page and leaves private reading alone. Changing a password signs out everyone who unlocked the page. Archiving hides a page from the library; it does not stop sharing.

Visitors see the page, its byline and the author's photo, and its images. They never see your request, its summary or source, your library, or the file the page was made from: Download original is offered only to you.

## Security model

Sharing opens one page to anyone with its link. It opens nothing else: the public listener has no library, Settings, editor or API, and its 404 looks the same for a missing page, a private page and a private route.

The private library is a different matter. It has no sign-in, so anyone and any software that can reach its address can read every page and use every owner form, including the ones that share pages, change passwords and make agent keys. Agent keys give each agent its own byline and keep the API from changing other authors' pages; they do not protect the library from an agent that can reach the private address. Only run agents you trust on machines that can reach it, and keep the private listener on loopback or your own tailnet. An agent can replace the pages it wrote, including ones you have shared, and visitors see the new version at once.

One wrong password costs nothing, so a visitor who mistypes and retypes gets straight in. From the second wrong password in a row the next try from the same address waits 1 second, then 2, 4 and so on up to 5 minutes, and the page says how long. Every wrong password also slows the page for everyone, but never by more than 10 seconds, so someone guessing cannot lock your readers out. Passwords set before Cairn asked for 8 characters still work; Manage page suggests setting a new one.

[SECURITY.md](../SECURITY.md#security-model) has the full model.

## Themes on shared pages

Shared pages use the theme chosen in Settings → Appearance, or the one set for shared pages under **Customise per view**. With System, each visitor's own device decides between Cairn and Dark; Cairn stores nothing about them. Pages written in Markdown take the theme's colours; pages in a design of their own keep it.

## Direct files and short links

A shared file has a reader and a direct address, such as `/abc.png`. Image, PDF, video and audio addresses show inline. HTML and SVG use a CSP sandbox with an opaque origin, `connect-src 'none'` and `form-action 'none'`. Other binary types download as attachments. Direct addresses check sharing and passwords just like the reader. Wrong extensions and unshared items return 404. Range requests support seeking and partial downloads.

Short links redirect with HTTP 302 and an empty body. GET visits increment the click count; HEAD does not. They have no password option. Stopping sharing disables public redirects while the owner can still edit the target privately.

Public page and file readers include Open Graph and Twitter preview tags. Protected pages and files show only a generic title and Cairn's preview image, even after unlocking. Original requests never appear in preview metadata.

## A private domain

A private reverse proxy can use `CAIRN_ALLOWED_HOSTS` to allow its hostname and `CAIRN_PRIVATE_BASE_URL` to set the private addresses returned by the API. Keep that proxy reachable only by your trusted devices, for example through Tailscale. Neither variable adds authentication or changes the public listener. See the installation guide for host configuration.

The private listener has no absolute upload or download deadline. The public listener keeps a 30-second read timeout for password forms and no write deadline for downloads. Both listeners bound headers to five seconds and idle connections to 60 seconds. Configure proxy transfer limits to suit your connection.
