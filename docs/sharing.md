# Public sharing

Public sharing is optional. Private reading and agent publishing work without a public domain.

Cairn uses two listeners. Port 8080 in the examples is the private library, editor, Settings and publishing API. Port 8081 serves only shared pages and their content, downloads and assets. Never route a public hostname to the private listener.

## Configure the share-only listener

Use a hostname you control, such as `reports.example.com`. In `/etc/cairn/cairn.env`, set:

```sh
CAIRN_LISTEN=127.0.0.1:8080
CAIRN_PUBLIC_LISTEN=127.0.0.1:8081
CAIRN_PUBLIC_BASE_URL=https://reports.example.com
```

Restart Cairn. For a foreground process, use `-public-listen 127.0.0.1:8081 -public-base-url https://reports.example.com` alongside the usual data and credential flags.

Configure your HTTPS reverse proxy to forward that hostname to `http://127.0.0.1:8081`. Preserve the original Host header and disable caching. Password cookies require HTTPS. Cairn does not provision DNS, certificates or proxy services.

A Cloudflare Tunnel can route the hostname to `http://127.0.0.1:8081`; configure cache bypass for that hostname. For a Caddy server on the same host, a minimal example is:

```caddyfile
reports.example.com {
    reverse_proxy 127.0.0.1:8081
}
```

Configure your DNS and firewall for the proxy you choose. The backend remains bound to loopback. Do not share the data directory with a static file server.

## Verify before sharing

The public root, `/settings`, `/new`, `/import`, `/api` and `/api/pages` must return 404. The root deliberately has no index of public reports.

Create a disposable page privately. Enable sharing in Manage page and check that the same path works on the public domain. Add an image and verify it loads. Set a password and test using a fresh browser session. Turn sharing off: the public reader, content, images and original download must stop working, while the private address still works. Delete the disposable page afterward.

## What a public link grants

Generated paths use three mixed-case alphanumeric characters by default. The owner can select 3 to 7 for future pages. Short URLs are discoverable, not secrets. Use a password if the shared content needs one. Password sharing is one shared password, not individual accounts.

Sharing keeps the private path on the public origin. Older custom public aliases remain supported. Unsharing disables both public routes and preserves private reading. Changing a password invalidates existing unlock cookies. Archiving hides a page from the main library; it does not revoke a public share.

Original request metadata never appears in the public reader. The report body, author byline/photo, images and imported original can be public. Check the original file as well as the rendered report before sharing.
