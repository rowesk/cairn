# Troubleshooting

| Symptom | Check |
| --- | --- |
| Cannot open the library | Confirm the process/service is running and that you are using the private listener. On another device, check Tailscale reachability and its access policy. |
| Public root returns 404 | Expected. Public access is only through a shared page's path. |
| Agent receives 401 | Check its key is active and the Authorization header is present. Per-agent revocation does not revoke the installation token. |
| Agent receives 403 on replacement | Its key can replace only pages with its exact author name. Browser-origin API requests are also refused. |
| Publication returns 409 | The path is already used or reserved. Use another path or explicitly replace the intended page. Do not blindly retry creation after a timeout. |
| Publication returns 413 | The JSON request exceeds 16 MiB, including base64 image overhead. Reduce the image payload. |
| Address already in use | Select an unused loopback port and update Tailscale/proxy configuration to match. |
| Listener address is refused | Use localhost or a Tailscale address assigned to the server. Do not use 0.0.0.0. |
| Data directory is already in use | Another Cairn process owns it. Stop that process; do not remove the lock to bypass it. |
| Report images or charts do not load through a proxy | Preserve Host, disable proxy caching, and inspect browser CSP errors. Remote images and scripts are intentionally blocked. |
| Browser cannot publish | Use an HTTP client outside the report/browser origin. This restriction prevents a report from publishing pages itself. |
| Init cannot find an existing credential | Pass the installation's original token file. Setup refuses to silently replace missing credentials for existing data. |

For systemd, inspect `sudo systemctl status cairn` and `sudo journalctl -u cairn`. Include `cairn version`, architecture, operating system and the failing action in a bug report. Remove tokens, passwords, original requests and private URLs before posting logs.
