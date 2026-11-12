# Proposal: an optional owner sign-in

Status: a proposal for the owner to decide. Nothing here is built.

## Why it comes up

The private library has no sign-in. Whoever can reach its address is the owner: they can read every page and request, share and delete pages, change share passwords and make agent keys. Agent keys limit what the publishing API does, not what the owner's forms do, so an agent running on a machine that can reach the library can read another author's page, rotate another agent's key or share a page by scraping the form token from any page and sending it back with a matching Origin header. A security review did exactly that with curl.

Today the answer is the network: keep the private listener on loopback or a tailnet of your own devices, and run only agents you trust there. That holds for one person with a laptop and a phone. It stops holding once an agent you trust less, a shared machine or a guest device can reach the address.

## What a sign-in would protect

- Owner pages and forms: the library, Manage, Settings, the editor, import and the private reader.
- Original requests, which only the owner should read.
- Sharing, deletion, passwords and agent keys.

It would not protect the publishing API, which already needs a key, the public listener, which serves only shared pages, or the data directory on disk.

## The simplest design

1. `cairn login` on the server prints a one-time link, such as `https://cairn.example.ts.net/_login/<32 random bytes>`, valid for ten minutes. No password is ever stored.
2. Opening the link sets a session cookie: `HttpOnly`, `SameSite=Strict`, `Secure` when the address is HTTPS, `Path=/`, lasting 90 days, and holding a random id that the server keeps a hash of. The link then stops working.
3. Every private browser route checks the cookie. Without it, the page explains how to run `cairn login`. `/api` keeps using keys and `/healthz` stays open.
4. Form tokens are bound to the session (an HMAC of the session id), so a token scraped from one session is useless in another.
5. Settings lists signed-in devices with their last use and a Sign out button for each; `cairn logout --all` ends every session.

The setting `CAIRN_OWNER_LOGIN=on|off`. About 250 lines of Go and a page of docs.

## What it costs

- On a phone: one extra step per device, every 90 days. You run `cairn login` on the server (or ask it to send the link to your Kindle address or email), then open the link on the phone. Safari's private browsing and cleared site data sign you out.
- On a new laptop or browser: the same step.
- Agents that drive a browser against the library stop working without a session. That is the point.
- `tailscale serve` with HTTPS keeps `Secure` cookies working. Plain `http://100.x.y.z` addresses work too, with a non-`Secure` cookie.
- Recovery when every session is lost: `cairn login` on the server always works, because having a shell there already means owning the data.

## Off or on by default

Off by default keeps today's behaviour for existing installs and means nothing changes on upgrade. On by default protects new installs whose owners never read this page, at the price of one more setup step that `cairn init` can print.

## Recommendation

Build it, on by default for new installations and off for existing ones until the owner turns it on, with `cairn init` printing the first sign-in link. The phone cost is one step per 90 days, the code is small, and it turns "anything that can reach the address is the owner" into "only devices you signed in are the owner", which is the claim people already assume an agent key makes. Until then, keep agents you do not fully trust on machines that cannot reach the private listener.
