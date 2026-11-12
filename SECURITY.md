# Security

Use GitHub's private vulnerability reporting on the Security tab to report a vulnerability. Do not post credentials or private report content in public issues. Supported security fixes target the latest release; there is no guaranteed response time.

## Security model

Cairn has one owner and no sign-in. Whoever can reach the private listener is the owner.

- The private listener has no application login. Any person or program that can reach its address can read every page and every original request, and can use every owner form: share, unshare, delete, change passwords, and create, rotate or revoke agent keys. A form token and an Origin check stop other websites from doing this through your browser. They do not stop software running on a machine that can reach the address, which can read the token from any page.
- An agent key is a byline, not a lock. It gives each agent its own name on its pages, and it keeps the publishing API from reading or changing other authors' pages. An agent that also has network access to the library can do everything the owner can, key or no key. Run only agents you trust on machines that can reach the private address.
- An agent key can replace the pages it wrote, including pages you have shared, and the new version is public at once. Settings will not make a key for your own default author name.
- The network boundary is the lock. Keep the private listener on loopback or a Tailscale address, and keep your tailnet's access rules to devices you control. Do not expose it to the public internet, a LAN, or a tailnet with untrusted members.
- Each listener answers only the host names its owner uses, so a website cannot reach the library by pointing its own name at your machine (DNS rebinding). The private listener accepts IP addresses, `localhost`, single-label tailnet names, names under `.ts.net`, the host of `CAIRN_PRIVATE_BASE_URL` and anything in `CAIRN_ALLOWED_HOSTS`. The public listener accepts the host of `CAIRN_PUBLIC_BASE_URL` and loopback.

An optional owner sign-in would change this model. [docs/owner-login-proposal.md](docs/owner-login-proposal.md) sets out what it would protect and what it would cost.

## Shared pages

The public listener serves explicitly shared pages only. Short public paths are discoverable. Password protection is optional and uses one shared password. After repeated wrong passwords, the same address waits longer before each new try, up to 5 minutes, and everyone else waits at most 10 seconds. A single wrong password costs no wait. Shared file pages offer their file behind the same sharing and password checks. Other pages keep their source owner-only.

## The host and its data

The data directory contains private request metadata and credentials. Use a dedicated OS account, preserve backup permissions, and keep it away from untrusted local users. Cairn assumes its data directory and host are trusted. It does not provide encrypted storage, multi-user isolation or version history.

## Page content

Pages can run scripts inside an opaque-origin iframe sandbox. Remote scripts, network requests, forms and access to the library document are restricted by CSP. Keep your reverse proxy's Host forwarding and no-cache settings intact. Test both listeners after changing proxy configuration.

Publish privately by default. Set `share_enabled` to `true` on `POST /api/pages` only when the owner asked for this page to be shared. Never set it otherwise. Tell the owner the saved `public_url` afterwards, as well as the private address. Sharing must be configured or creation fails with 503 `public_sharing_unavailable` without saving. Omitted or `false` stays private. Any supplied value on PUT is refused; replacement preserves sharing, link names and passwords. Files start private.
