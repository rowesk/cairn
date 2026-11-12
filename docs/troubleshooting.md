# Troubleshooting

Cairn's error messages say what went wrong and what to do next, and name the flag or environment variable involved. Start there. For the service, read them with `sudo journalctl -u cairn -n 30 --no-pager`. The lines Cairn prints at start show the version, data directory, private address, and whether sharing and Send to Kindle are on.

## Access

| Symptom | Check |
| --- | --- |
| There is no login screen | Expected. Cairn has no accounts or passwords for the library. Anyone, and any program, that can reach the private listener can read and manage every page, agent key or not, so the network boundary is the lock: loopback on the machine itself, or your tailnet and its access policy. Keep the private listener off LAN and public addresses. Cairn refuses them for this reason. Passwords only protect individual shared pages. See [SECURITY.md](../SECURITY.md#security-model). |
| Cannot open the library | Confirm Cairn is running and that you are using the private address it printed at start. On another device, check Tailscale reachability and its access policy. |
| Cairn says this address is not allowed | The page reads "This address isn’t allowed" and the request gets `421`. Cairn answers only names it knows, so a website cannot reach your library by pointing its own name at your machine. IP addresses, `localhost`, single-label tailnet names such as `raspberrypi`, names under `.ts.net` and the host of `CAIRN_PRIVATE_BASE_URL` always work. To open Cairn at another name, such as `cairn.home.example`, add it to `CAIRN_ALLOWED_HOSTS` (several names separated by commas) or `-allowed-hosts`, or set `CAIRN_PRIVATE_BASE_URL` to that address, then restart Cairn. The log line `host refused` names the host that was turned away. |
| Shared links say "This link isn’t working" for every page | If the log shows `host refused` with `listener=public`, your proxy sends a Host header Cairn does not expect. Make it pass the original Host, which must match `CAIRN_PUBLIC_BASE_URL`, or the loopback address it connects to. |
| Public root returns 404 | Expected. Public access is only through a shared page's path. |
| Report images or charts do not load through a proxy | Preserve Host, disable proxy caching, and inspect browser CSP errors. Remote images and scripts are intentionally blocked. |

## Starting Cairn

| Symptom | Check |
| --- | --- |
| "Unknown command" or "Unexpected argument" | Commands go first and flags after: `cairn init -data ./data`, not `cairn -data ./data init`. Quote values with spaces: `-owner "Alex Example"`. `cairn help` lists everything. |
| "Cairn needs an installation token" | The data directory has not been set up. Run the `cairn init` command the message prints, then start Cairn again. |
| "No installation token was found for the library" | The database exists but its token is not in the data directory. Pass the original token with `-publisher-token-file` or `CAIRN_PUBLISHER_TOKEN`. |
| "Cairn cannot read the installation token file" | Run Cairn as the user that owns the file. For the service that is the `cairn` user, so test by hand with `sudo -u cairn /usr/local/bin/cairn ...`. |
| "needs an IP address, not the name localhost" | Use `127.0.0.1:8080`. Cairn takes IP addresses only, so it knows exactly which network it listens on. |
| "Cairn will not listen on ..." | Cairn listens only on loopback or a Tailscale IP assigned to the machine. Use `127.0.0.1` with `tailscale serve`, or this machine's address from `tailscale ip -4`. Never use `0.0.0.0`. |
| "another program is using that port" | Pick a free loopback port with `-listen` or `CAIRN_LISTEN`, and update Tailscale or your proxy to match. |
| "Another Cairn process is already using the data directory" | Stop that process first. Do not remove the `.lock` file to get around it. |
| "is set but ... is not. Sharing needs both" | Set both `CAIRN_PUBLIC_LISTEN` and `CAIRN_PUBLIC_BASE_URL`, or leave both empty. See [sharing](sharing.md). |
| "Send to Kindle is only partly set up" | Set the mail server, sending address and password together, or clear them all. See [Send to Kindle](kindle.md). |
| "lists ..., which is not a host name" | `CAIRN_ALLOWED_HOSTS` and `-allowed-hosts` take names only, separated by commas: `cairn.home.example,mac-mini`, not `https://cairn.home.example/`. |
| A setting seems to be ignored | A flag wins over its environment variable, and empty values count as unset. The service passes `-data` and `-publisher-token-file` itself, so set everything else in `/etc/cairn/cairn.env`. |
| Init says it ignored `-owner` | The library already exists, so its author stays. Change the default author in Settings. |
| Init cannot find an existing token | Pass the installation's original token file. `init` will not replace the token of an existing library. |

## Publishing

| Symptom | Check |
| --- | --- |
| Agent receives 401 | `authentication_required` means no `Authorization: Bearer` header arrived: check `CAIRN_API_KEY` is set where the agent runs. `invalid_credential` means the key is wrong, rotated or revoked: check its row in Settings, Authors, and rotate it if needed. Revoking agent keys does not revoke the installation token. |
| Agent receives 403 | `forbidden`: the key asked to list another author's pages with `agent=`. `browser_origin_forbidden`: the request came from a browser. |
| Agent receives 404 for a page that exists | The page has another byline. An agent key reads and changes only its own pages, and answers for other authors' pages as if they did not exist. |
| Publication returns 409 | `slug_conflict`: the slug is taken, letter case aside; leave `slug` out. After a timeout, retry with the same `Idempotency-Key` so the page is not saved twice. |
| Publication returns 413 | The JSON request exceeds 16 MiB, including base64 image overhead. Reduce the image payload. |
| Settings says "This is your own name as the default author" | A key on your default author name would reach every page you write or import. Give the agent its own name, such as Claude or Codex. A key made on that name before this rule can still be revoked. |
| Browser cannot publish | Use an HTTP client outside the report or browser origin. This restriction stops a report from publishing pages itself. |
| Agent's links lack the address | Set `CAIRN_PRIVATE_BASE_URL` to the address you open the library at, so the API returns full `private_url` links. |

## Send to Kindle

| Symptom | Check |
| --- | --- |
| "Cairn couldn't reach the mail server" | Cairn got no answer from `CAIRN_SMTP_SERVER`: the server name or port is wrong, the network is down, or a firewall blocks it. Check the address in `/etc/cairn/cairn.env`, then press Try again. |
| "The mail server refused Cairn's sign-in" | The username or app password in `/etc/cairn/cairn.env` is wrong or was revoked. Fix it and restart Cairn; trying again before that fails the same way. |
| "The mail server refused the message" | The mail server turned the message away, most often because of the Kindle address. Check it in Settings, Send to Kindle. The reason is in `sudo journalctl -u cairn -n 30 --no-pager`. |
| "The mail server asked Cairn to try later" | The mail server is busy or limiting how much you send. Press Try again in a few minutes. |
| "Cairn couldn't send this page to your Kindle" | Something else went wrong, such as a mail server whose certificate does not match its name. Read the reason with `sudo journalctl -u cairn -n 30 --no-pager`; the line names the cause. |
| Cairn says it sent the page, but nothing arrives | Amazon has usually rejected the sender. Add the address in `CAIRN_MAIL_FROM` to the approved list, then look for a reply from Amazon in the sending account's inbox. See [Send to Kindle](kindle.md#tell-amazon-about-the-sender). |
| "This page is too large to email to a Kindle" | Amazon takes about 18 MB at most. Pages with many large images will not fit. |

## Reporting a bug

Include the output of `cairn version`, which gives the version, operating system and architecture, plus the failing action and the message Cairn printed. Remove tokens, passwords, original requests and private URLs before posting logs.
